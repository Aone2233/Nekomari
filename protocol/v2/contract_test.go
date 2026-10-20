package v2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

/*
两份协议副本的一致性契约

面板与 agent 是两个 Go 模块（根模块 / agent/），无法共享协议代码，于是 jsonrpc.go 存在两份
副本：protocol/v2/jsonrpc.go 与 agent/protocol/v2/jsonrpc.go。它们之间没有任何编译期联系 ——
漂移的表现是"事件发出去了但对面说 unknown method"，或者"字段名在一侧改了，另一侧默默忽略，
数据缺一列"，都不会报错。

v1.6.24 加 value_us 时就是手工改两遍。本文件把"必须同步的东西"变成断言：

 1. agent 构造的 ping 负载字段集合 == 面板 PingResultParams 的 JSON tag 集合；
 2. 两侧的协议常量（Version 与全部 Method*）名字与取值一致；
 3. 两份副本都声明的结构体，其字段名+JSON tag 完全一致（只比线缆形状，不比 Go 类型，
    因为 interface{} 与 any 这类差异不影响协议）。

用 go/parser 解析源码而不是正则：正则对注释、换行、字段顺序太敏感，而这些断言要长期留在
CI 里。解析不到预期结构时测试直接失败并说明"解析假设破了"，不允许默默通过。
*/

const (
	// panelProtocolFile 是面板侧协议副本（本包）。
	panelProtocolFile = "protocol/v2/jsonrpc.go"
	// agentProtocolFile 是 agent 侧协议副本（另一个模块，只能用源码解析来比较）。
	agentProtocolFile = "agent/protocol/v2/jsonrpc.go"
)

// requiredPingWireFields 是 ping 结果负载必须同时出现在两侧的字段。
//
// 单独列出来是为了防止"两边一起删"这种情况：集合相等只能发现单侧漂移，若某个字段从两份
// 副本同时消失，集合比较依然通过，而这个功能其实已经没了。
var requiredPingWireFields = []string{
	"task_id", "ping_type", "value", "value_us", "finished_at", "role", "family",
}

// repoFile 从当前包的目录向上找到 go.mod，再拼出仓库内相对路径。
//
// 测试的 cwd 是包目录（protocol/v2），而 agent 副本在另一棵子树里，所以只能从仓库根定位。
func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(append([]string{dir}, parts...)...)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// parseProtocolFile 解析一份协议副本。
func parseProtocolFile(t *testing.T, relPath string) *ast.File {
	t.Helper()
	path := repoFile(t, filepath.FromSlash(relPath))
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

// structWireFields 返回一个结构体的字段名到 JSON 线缆名的映射。
//
// 第二个返回值为 false 表示该类型没有在这个文件里声明 —— 调用方需要区分"结构体为空"
// 与"结构体不存在"，否则拼写错误会让断言静默通过。
func structWireFields(t *testing.T, file *ast.File, typeName string) (map[string]string, bool) {
	t.Helper()
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != typeName {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s in %s is not a struct", typeName, file.Name.Name)
			}
			fields := make(map[string]string, len(structType.Fields.List))
			for _, field := range structType.Fields.List {
				wire := jsonWireName(field)
				for _, name := range field.Names {
					fields[name.Name] = wire
				}
			}
			return fields, true
		}
	}
	return nil, false
}

// jsonWireName 取出一个结构体字段 tag 里的 JSON 名（去掉 omitempty 之类的选项）。
func jsonWireName(field *ast.Field) string {
	if field.Tag == nil {
		return ""
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return ""
	}
	tag := reflect.StructTag(raw).Get("json")
	if tag == "" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return ""
	}
	return name
}

// stringConstants 收集文件中所有以字符串字面量赋值的常量。
func stringConstants(file *ast.File) map[string]string {
	constants := make(map[string]string)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range valueSpec.Names {
				if i >= len(valueSpec.Values) {
					continue
				}
				lit, ok := valueSpec.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				constants[name.Name] = value
			}
		}
	}
	return constants
}

// pingPayloadWireKeys 从 agent 的 buildPingResultPayload 里提取它写进 params 的所有键。
//
// 所有 ping 构建函数（旧的无 role 版本、带 role/family 的版本、带微秒的版本）都收敛到这一个
// 私有函数，所以它写出的键就是 agent 实际会发到线缆上的字段集合。
func pingPayloadWireKeys(t *testing.T, file *ast.File) map[string]bool {
	t.Helper()
	var builder *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "buildPingResultPayload" {
			builder = fn
		}
	}
	if builder == nil {
		t.Fatal("agent 侧找不到 buildPingResultPayload：解析假设已被打破，请同步更新本测试")
	}

	keys := make(map[string]bool)
	record := func(expr ast.Expr) {
		lit, ok := expr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		if key, err := strconv.Unquote(lit.Value); err == nil {
			keys[key] = true
		}
	}
	ast.Inspect(builder, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.KeyValueExpr:
			// map[string]interface{}{"task_id": ...}
			record(n.Key)
		case *ast.IndexExpr:
			// params["role"] = role
			record(n.Index)
		}
		return true
	})
	return keys
}

func sortedWireKeys(keys map[string]bool) []string {
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sortedFields(fields map[string]string) []string {
	out := make([]string, 0, len(fields))
	for name, wire := range fields {
		out = append(out, name+"->"+wire)
	}
	sort.Strings(out)
	return out
}

// TestAgentPingPayloadMatchesThePanelDecoder 是本次改动的核心契约：
//
//	agent 写出的 params 键  ==  面板 PingResultParams 的 JSON tag
//
// 任何一侧单独改动（例如只给 agent 加了 value_us，或只给面板加了字段）都会在这里失败，
// 并指出是哪一侧、哪个字段。
func TestAgentPingPayloadMatchesThePanelDecoder(t *testing.T) {
	agentKeys := pingPayloadWireKeys(t, parseProtocolFile(t, agentProtocolFile))
	// 解析不到足够多的键说明这份测试的假设已经跟不上源码了：宁可失败，也不要空集合下通过。
	if len(agentKeys) < len(requiredPingWireFields) {
		t.Fatalf("只从 buildPingResultPayload 里提取到 %d 个键（%v），少于预期的 %d 个："+
			"解析假设已被打破，请同步更新本测试", len(agentKeys), sortedWireKeys(agentKeys), len(requiredPingWireFields))
	}

	panelFields, ok := structWireFields(t, parseProtocolFile(t, panelProtocolFile), "PingResultParams")
	if !ok {
		t.Fatal("面板侧找不到 PingResultParams：解析假设已被打破，请同步更新本测试")
	}
	panelKeys := make(map[string]bool, len(panelFields))
	for _, wire := range panelFields {
		if wire != "" {
			panelKeys[wire] = true
		}
	}

	for _, required := range requiredPingWireFields {
		if !panelKeys[required] {
			t.Errorf("面板 PingResultParams 缺少 %q：agent 会发这个字段，面板会静默丢弃它", required)
		}
		if !agentKeys[required] {
			t.Errorf("agent 的 ping 负载没有写 %q：面板在等这个字段", required)
		}
	}

	for _, problem := range comparePingWireKeys(agentKeys, panelKeys) {
		t.Error(problem)
	}
}

// comparePingWireKeys 返回两侧字段集合的漂移描述（空表示一致）。
//
// 单独抽出来是为了让它自己也被测到（见 TestContractComparisonDetectsDrift）：一个永远
// 不报错的比较函数，写起来最容易、留在 CI 里最没用。
func comparePingWireKeys(agentKeys, panelKeys map[string]bool) []string {
	var problems []string
	for _, key := range sortedWireKeys(agentKeys) {
		if !panelKeys[key] {
			problems = append(problems, "agent 上报的字段 "+key+" 不在 PingResultParams 里：面板会忽略它（数据静默缺失）")
		}
	}
	for _, key := range sortedWireKeys(panelKeys) {
		if !agentKeys[key] {
			problems = append(problems, "PingResultParams 声明了 "+key+"，但 agent 的 ping 负载从不写它：面板永远读到零值")
		}
	}
	return problems
}

// TestContractComparisonDetectsDrift 是上面三条断言的自检：
//
// 它用合成源码与合成集合证明"漂移真的会被报出来"。没有这一段，本文件最可能的失败模式
// 是"解析假设过时、提取到空集合、然后一切通过"。
func TestContractComparisonDetectsDrift(t *testing.T) {
	if problems := comparePingWireKeys(map[string]bool{"value_us": true}, map[string]bool{"value_us": true}); len(problems) != 0 {
		t.Fatalf("identical sets reported as drift: %v", problems)
	}
	if problems := comparePingWireKeys(map[string]bool{"value_us": true}, map[string]bool{"value": true}); len(problems) != 2 {
		t.Fatalf("one-sided field produced %d problems (%v), want 2", len(problems), problems)
	}

	// 合成一份"agent 加了 value_us、解析器必须看得见"的源码。
	const syntheticAgent = `package v2

func buildPingResultPayload() interface{} {
	params := map[string]interface{}{
		"task_id": 1,
	}
	params["value_us"] = 1
	return params
}`
	file, err := parser.ParseFile(token.NewFileSet(), "synthetic_agent.go", syntheticAgent, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse synthetic source: %v", err)
	}
	keys := pingPayloadWireKeys(t, file)
	if !keys["task_id"] || !keys["value_us"] || len(keys) != 2 {
		t.Fatalf("extracted keys = %v, want task_id + value_us", sortedWireKeys(keys))
	}
}

// TestProtocolCopiesAgreeOnConstants 比较两份副本里的协议常量（Version 与全部 Method*）。
//
// 常量漂移的后果是"事件发出去了，对面说 unknown method" —— 服务端和 agent 各自都自洽，
// 只有跨模块比较才能发现。
func TestProtocolCopiesAgreeOnConstants(t *testing.T) {
	panel := stringConstants(parseProtocolFile(t, panelProtocolFile))
	agent := stringConstants(parseProtocolFile(t, agentProtocolFile))

	for name, value := range panel {
		agentValue, ok := agent[name]
		if !ok {
			t.Errorf("面板声明了常量 %s=%q，agent 侧没有：发出去的事件对面不认识", name, value)
			continue
		}
		if agentValue != value {
			t.Errorf("常量 %s 漂移：面板 %q，agent %q", name, value, agentValue)
		}
	}
	for name, value := range agent {
		if _, ok := panel[name]; !ok {
			t.Errorf("agent 声明了常量 %s=%q，面板侧没有：这份协议只能单方向工作", name, value)
		}
	}
}

// TestSharedStructsKeepIdenticalWireFields 比较两份副本都声明过的结构体。
//
// 只比字段名与 JSON tag，不比 Go 类型：interface{} 与 any、uint 与 int 这类差异不影响
// 线缆形状，把它们算进去只会让测试在无害重构上失败。
func TestSharedStructsKeepIdenticalWireFields(t *testing.T) {
	panelFile := parseProtocolFile(t, panelProtocolFile)
	agentFile := parseProtocolFile(t, agentProtocolFile)

	panelTypes := structTypeNames(panelFile)
	agentTypes := structTypeNames(agentFile)

	for _, name := range sortedTypeNames(panelTypes) {
		if !agentTypes[name] {
			continue
		}
		panelFields, _ := structWireFields(t, panelFile, name)
		agentFields, _ := structWireFields(t, agentFile, name)

		for field, panelWire := range panelFields {
			agentWire, ok := agentFields[field]
			if !ok {
				t.Errorf("%s.%s 在面板侧存在、agent 侧缺失：agent 解不出这个字段", name, field)
				continue
			}
			if agentWire != panelWire {
				t.Errorf("%s.%s 的 JSON 名漂移：面板 %q，agent %q", name, field, panelWire, agentWire)
			}
		}
		for field := range agentFields {
			if _, ok := panelFields[field]; !ok {
				t.Errorf("%s.%s 在 agent 侧存在、面板侧缺失：面板解不出这个字段", name, field)
			}
		}

		if t.Failed() {
			t.Logf("面板侧 %s 字段：%v", name, sortedFields(panelFields))
			t.Logf("agent 侧 %s 字段：%v", name, sortedFields(agentFields))
		}
	}
}

// structTypeNames 收集文件里声明的结构体类型名。
func structTypeNames(file *ast.File) map[string]bool {
	names := make(map[string]bool)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, ok := typeSpec.Type.(*ast.StructType); ok {
				names[typeSpec.Name.Name] = true
			}
		}
	}
	return names
}

func sortedTypeNames(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
