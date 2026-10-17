package plugin

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Aone2233/nekomari/utils"
)

// CheckKomariVersion validates a manifest komari constraint against the
// running server version. Supported constraints: empty (any version),
// "x.y.z" (exact), and ">=x.y.z", ">x.y.z", "<=x.y.z", "<x.y.z". An optional
// leading "v" is accepted.
func CheckKomariVersion(constraint string) error {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return nil
	}
	rest := constraint
	op := ""
	for _, candidate := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(rest, candidate) {
			op = candidate
			rest = strings.TrimSpace(strings.TrimPrefix(rest, candidate))
			break
		}
	}
	want, err := parseSemver(rest)
	if err != nil {
		return fmt.Errorf("invalid komari version constraint %q: %w", constraint, err)
	}
	have, err := parseSemver(utils.CurrentVersion)
	if err != nil {
		// 版本不可解析时不能假装满足。约束是插件在声明「我需要某个 API」，而无法
		// 比较就意味着无法确认 —— 放行等于把这道门静默关掉。
		//
		// 这里曾经 return nil（注释写的是「格式不对的服务端版本不该拦住插件加载」），
		// 而面板那一端当时会判为不满足，于是两端相反；后来面板也改成了放行，
		// 结果是两端一致地放行 —— 也就是整道门在版本号带后缀时失效。现在两端都
		// 拒绝并说明原因。空约束仍然直接放行（见函数开头）。
		return fmt.Errorf("plugin requires komari %s, but the running server version %q cannot be compared: %w",
			constraint, utils.CurrentVersion, err)
	}
	if !satisfies(compareSemver(have, want), op) {
		return fmt.Errorf("plugin requires komari %s, running %s", constraint, utils.CurrentVersion)
	}
	return nil
}

func satisfies(cmp int, op string) bool {
	switch op {
	case ">=":
		return cmp >= 0
	case ">":
		return cmp > 0
	case "<=":
		return cmp <= 0
	case "<":
		return cmp < 0
	default: // exact
		return cmp == 0
	}
}

func parseSemver(s string) ([3]int, error) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "v"))
	if s == "" {
		return [3]int{}, fmt.Errorf("empty version")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return [3]int{}, fmt.Errorf("too many version parts")
	}
	var v [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return [3]int{}, fmt.Errorf("invalid version part %q", part)
		}
		v[i] = n
	}
	return v, nil
}

func compareSemver(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
