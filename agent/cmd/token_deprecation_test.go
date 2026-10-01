package cmd

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
)

// captureLog collects what a warning writes to the standard logger.
//
// The logger is process-global, so it is always restored: a warning test that
// left `log` pointed at a closed buffer would break every later test in the
// package in a way that looks unrelated to what it tested.
func captureLog(t *testing.T, write func()) string {
	t.Helper()
	previousOut := log.Writer()
	previousFlags := log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousOut)
		log.SetFlags(previousFlags)
	}()
	write()
	return buf.String()
}

// withArgs runs fn with os.Args replaced, as if the process had been started
// with those arguments.
func withArgs(t *testing.T, args []string, fn func()) {
	t.Helper()
	saved := os.Args
	os.Args = args
	defer func() { os.Args = saved }()
	fn()
}

// 弃用警告：每一种写法都要说「deprecated」，都要指向 --token-file，都要不含 token 值，
// 且都不能给出移除期限 —— 面板生成的安装命令至今仍写 `-t`，宣布期限等于承诺一次
// 没有排期的破坏。
func TestTokenFlagDeprecationWarningPointsAtTokenFileOnEverySpelling(t *testing.T) {
	const secret = "s3cr3t-value-9b2e77-never-printed"

	cases := []struct {
		name string
		args []string
	}{
		{name: "short separate", args: []string{"komari-agent", "-t", secret}},
		{name: "short attached", args: []string{"komari-agent", "-t" + secret}},
		{name: "short equals", args: []string{"komari-agent", "-t=" + secret}},
		{name: "long separate", args: []string{"komari-agent", "--token", secret}},
		{name: "long equals", args: []string{"komari-agent", "--token=" + secret}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withArgs(t, tc.args, func() {
				out := captureLog(t, warnIfTokenFlagDeprecated)
				if out == "" {
					t.Fatal("using -t/--token printed no deprecation warning")
				}
				if !strings.Contains(out, "deprecated") {
					t.Fatalf("warning does not say the flag is deprecated: %q", out)
				}
				if !strings.Contains(out, "--token-file") {
					t.Fatalf("warning does not point at --token-file: %q", out)
				}
				if strings.Contains(out, secret) {
					t.Fatalf("warning printed the token value: %q", out)
				}
				// 不设移除期限：既不能带版本号（本仓库另外两条弃用警告就是这个写法），
				// 也不能出现 removed / will be removed。
				for _, deadline := range []string{"deprecated in version", "will be removed", "removed in"} {
					if strings.Contains(out, deadline) {
						t.Fatalf("warning announces a removal deadline (%q): %q", deadline, out)
					}
				}
			})
		})
	}
}

// 把两条 token 警告放在一起跑：既有的暴露警告与新的弃用警告都不能把值写进日志。
// 暴露警告报的是「多少个字符」，那正是它安全的理由，这里一并钉住。
//
// 每一种写法都要过一遍：`-tv` 这条正是本轮补上的洞（贴合写法原先两条警告都绕过），
// 它的值也必须进泄露检查，不能只把分开写的 `-t v` 检查一遍。
func TestTokenWarningsNeverPrintTheTokenValue(t *testing.T) {
	const secret = "s3cr3t-value-4f8a1c-never-printed"

	cases := []struct {
		name string
		args []string
	}{
		{name: "short separate", args: []string{"komari-agent", "-e", "https://panel.example", "-t", secret}},
		{name: "short attached", args: []string{"komari-agent", "-e", "https://panel.example", "-t" + secret}},
		{name: "short equals", args: []string{"komari-agent", "-t=" + secret}},
		{name: "long equals", args: []string{"komari-agent", "--token=" + secret}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withArgs(t, tc.args, func() {
				saved := tokenOnCommandLine
				defer func() { tokenOnCommandLine = saved }()
				tokenOnCommandLine = os.Getenv("AGENT_TOKEN") == "" && tokenFlagProvided(os.Args)
				if !tokenOnCommandLine {
					t.Fatalf("the detector missed %v, so neither warning would fire", tc.args)
				}

				out := captureLog(t, func() {
					warnIfTokenOnCommandLine()
					warnIfTokenFlagDeprecated()
				})
				if strings.Contains(out, secret) {
					t.Fatalf("a token warning printed the value: %q", out)
				}
				if !strings.Contains(out, fmt.Sprintf("%d characters", len(secret))) {
					t.Fatalf("the exposure warning no longer reports the length instead of the value: %q", out)
				}
				if !strings.Contains(out, "--token-file") {
					t.Fatalf("neither warning points at --token-file: %q", out)
				}
				if !strings.Contains(out, "deprecated") {
					t.Fatalf("no deprecation warning was printed: %q", out)
				}
			})
		})
	}
}

// 反向的一半：token 来自文件时那两条警告都不该出现。`--token-file` 以 `--token` 开头，
// 用前缀匹配就会误报 —— 这条把它钉住。
func TestTokenWarningsAreSilentWithoutTheTokenFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "token file", args: []string{"komari-agent", "--token-file", "/etc/komari/agent-token", "-e", "https://panel.example"}},
		{name: "token file equals", args: []string{"komari-agent", "--token-file=/etc/komari/agent-token"}},
		{name: "no token at all", args: []string{"komari-agent", "-e", "https://panel.example", "-i", "5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withArgs(t, tc.args, func() {
				saved := tokenOnCommandLine
				defer func() { tokenOnCommandLine = saved }()
				tokenOnCommandLine = os.Getenv("AGENT_TOKEN") == "" && tokenFlagProvided(os.Args)

				out := captureLog(t, func() {
					warnIfTokenOnCommandLine()
					warnIfTokenFlagDeprecated()
				})
				if out != "" {
					t.Fatalf("warnings fired without -t/--token: %q", out)
				}
			})
		})
	}
}

// 检测器必须与 pflag 的真实解析一致：它喂的是两条 token 警告，而决定
// `komari-agent` 到底把什么当成 token 的是 pflag。所以这张表不写死期望值，而是拿
// 真实的 RootCmd 去 ParseFlags，再断言「检测器说用了 -t」与「pflag 真的填上了
// flags.Token」一致、且两者取到的值逐字相同。这样 pflag 的行为或检测器的实现任一
// 变化都会被抓到，而不是被一份手抄的期望值掩盖。
//
// `-token-file x` 是有意放进来的一格：pflag 把单横线参数读作 shorthand 簇，所以它
// 等价于 `-t oken-file`（长名必须写 `--token-file`）。检测器必须跟着一致 —— 否则
// 一个把 token 真的放上命令行的写法又绕过了两条警告。
func TestCommandLineTokenMatchesPflagForEverySpelling(t *testing.T) {
	const secret = "pflag-table-token-value"

	cases := []struct {
		name string
		args []string
	}{
		{name: "short separate", args: []string{"-t", secret}},
		{name: "short attached", args: []string{"-t" + secret}},
		{name: "short equals", args: []string{"-t=" + secret}},
		{name: "short empty equals", args: []string{"-t="}},
		{name: "long separate", args: []string{"--token", secret}},
		{name: "long equals", args: []string{"--token=" + secret}},
		{name: "token file long", args: []string{"--token-file", "/etc/komari/agent-token"}},
		{name: "token file short-looking", args: []string{"-token-file", "x"}},
		{name: "no token", args: []string{"-e", "https://panel.example", "-i", "5"}},
		{name: "dangling short", args: []string{"-t"}},
		{name: "after terminator", args: []string{"--", "-t", secret}},
		{name: "endpoint then token", args: []string{"-e", "https://panel.example", "-t", secret}},
	}

	saved := *flags
	defer func() { *flags = saved }()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// pflag only writes the flags it finds, so a token left over from the
			// previous case would look like a parse.
			*flags = saved
			RootCmd.SetArgs(tc.args)
			// A dangling `-t` is a real parse error; the detector has to agree that
			// no token was read rather than invent one.
			_ = RootCmd.ParseFlags(tc.args)

			got, used := commandLineToken(tc.args)
			pflagToken := flags.Token
			if used != (pflagToken != "") {
				t.Fatalf("commandLineToken(%v) says the flag was used=%v, but pflag parsed token %q",
					tc.args, used, pflagToken)
			}
			if used && got != pflagToken {
				t.Fatalf("commandLineToken(%v) = %q, but pflag parsed %q", tc.args, got, pflagToken)
			}
		})
	}
}

// 默认行为不变：--token 仍然注册在 root 命令上、仍然接受同一个 token，也仍然出现在
// --help 里。不使用 cobra 的 MarkDeprecated 是有意的 —— 那个调用会顺手把 flag 从
// --help 隐藏掉，而面板生成的安装命令至今仍打印 `-t`。
func TestTokenFlagStaysRegisteredAndVisible(t *testing.T) {
	flag := RootCmd.PersistentFlags().Lookup("token")
	if flag == nil {
		t.Fatal("--token is no longer registered on the root command")
	}
	if flag.Shorthand != "t" {
		t.Fatalf("shorthand = %q, want \"t\"", flag.Shorthand)
	}
	if flag.Deprecated != "" {
		t.Fatalf("--token carries pflag Deprecated=%q; MarkDeprecated also hides the flag from --help, which is more than a warning", flag.Deprecated)
	}
	if flag.Hidden {
		t.Fatal("--token is hidden from --help")
	}
	if RootCmd.PersistentFlags().Lookup("token-file") == nil {
		t.Fatal("--token-file, the replacement named by the warning, is not registered")
	}
	if _, ok := commandLineToken([]string{"komari-agent", "-t", "abc"}); !ok {
		t.Fatal("the -t spelling stopped being recognised")
	}
}
