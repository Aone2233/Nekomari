package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
	"syscall"

	"github.com/Aone2233/nekomari/agent/dnsresolver"
	"github.com/Aone2233/nekomari/agent/monitoring/netstatic"
	monitoring "github.com/Aone2233/nekomari/agent/monitoring/unit"
	"github.com/Aone2233/nekomari/agent/server"
	"github.com/Aone2233/nekomari/agent/update"
	"github.com/spf13/cobra"

	pkg_flags "github.com/Aone2233/nekomari/agent/cmd/flags"
)

var flags = pkg_flags.GlobalConfig

var warningPanelHost, warningRunAsUser string

// tokenOnCommandLine records whether this process was started with the token in
// its arguments. It is read only by the one-time warning, after the token itself
// is no longer reachable through this flag.
var tokenOnCommandLine bool

func tokenFlagProvided(args []string) bool {
	_, ok := commandLineToken(args)
	return ok
}

// warnIfTokenOnCommandLine states the exposure once, at startup, where the
// operator will see it. Not an error: `-t` is still what the panel's generated
// install command uses, and a node that refuses to start is a worse outcome than
// one that starts with a warning.
func warnIfTokenOnCommandLine() {
	if !tokenOnCommandLine {
		return
	}
	token, ok := commandLineToken(os.Args)
	if !ok {
		return
	}
	log.Printf("WARNING: the API token is on the command line (%d characters), so any local user can read it from `ps` or `systemctl show`. Move it to a file: `--token-file <path>` (mode 0600), or an AGENT_TOKEN environment file.", len(token))
}

// tokenDeprecationNotice is the one thing an operator using -t/--token has to
// change. Two properties are deliberate:
//
//   - No removal deadline. The panel's generated install command still writes
//     `-t`, so the flag has to keep working; announcing a version would promise a
//     break that is not scheduled.
//   - No token value, not even a prefix. Only the flag name and its replacement
//     are named, so this line is safe in a journal, a CI log or a bug report.
const tokenDeprecationNotice = "WARNING: --token/-t is deprecated. Anything passed on the command line is readable by every local user through `ps` and `systemctl show -p ExecStart`, so pass the token with --token-file <path> (mode 0600), or put AGENT_TOKEN in an environment file (AGENT_TOKEN_FILE works too). The flag keeps working and no removal date is set."

// warnIfTokenFlagDeprecated says, once, the part warnIfTokenOnCommandLine does
// not: the flag itself is on its way out. It is keyed on the same detector, so
// the exposure warning and the deprecation warning can never disagree about
// whether -t/--token was used.
func warnIfTokenFlagDeprecated() {
	if !tokenFlagProvided(os.Args) {
		return
	}
	log.Print(tokenDeprecationNotice)
}

// commandLineToken returns the token spelled out in args, or ok=false when the
// token is not there.
//
// It follows pflag's own parse order for the `-t` shorthand, because a spelling
// pflag accepts has to be recognised here too: anything that slips past this
// function slips past BOTH the exposure warning and the deprecation warning.
// `-tv` did exactly that — pflag reads a string shorthand as "the character after
// the dash is the flag, the rest is its value", so `-tv` is `-t v`, and
// `-token-file x` is `-t oken-file`, not the long `--token-file`.
//
//	pflag order, per argument        value
//	------------------------------   -------------------------
//	--token v                        next argument
//	--token=v                        inline value
//	-t=v                             value after '=' ("-t=" is "=")
//	-tv / -token-file x              everything after 't'
//	-t v                             next argument
//	--token-file, --anything-else    not this flag, left alone
//	--                               ends flag parsing here
//
// Two spellings pflag accepts are deliberately not chased, because following them
// means re-implementing its cluster walker and its per-flag value table — the kind
// of second parser this function exists to avoid: a `t` that only appears after a
// boolean shorthand in one cluster (`-ut v`, where `-u` is boolean and pflag keeps
// walking), and a `--token` that is really the *value* of an earlier flag
// (`-e --token x`). The panel, the installers and the docs spell the flag the ways
// listed above.
func commandLineToken(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			// pflag stops parsing flags at a bare `--`; everything after it is a
			// positional argument, even if it looks like `-t`.
			return "", false
		}
		if strings.HasPrefix(arg, "--") {
			switch {
			case arg == "--token":
				if i+1 < len(args) {
					return args[i+1], true
				}
				return "", false
			case strings.HasPrefix(arg, "--token="):
				return strings.TrimPrefix(arg, "--token="), true
			}
			continue
		}
		// A single-dash argument is a shorthand cluster. `t` is the only shorthand
		// in this CLI whose letter is 't', so an argument whose first shorthand is
		// not `t` cannot set the token — and pflag reads the rest of the argument as
		// that shorthand's value, which is why the forms below take the tail.
		if len(arg) < 2 || arg[0] != '-' || arg[1] != 't' {
			continue
		}
		rest := arg[1:]
		switch {
		case len(rest) > 2 && rest[1] == '=':
			return rest[2:], true
		case len(rest) > 1:
			return rest[1:], true
		case i+1 < len(args):
			return args[i+1], true
		}
		return "", false
	}
	return "", false
}

var RootCmd = &cobra.Command{
	Use:   "komari-agent",
	Short: "komari agent",
	Long:  `komari agent`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Notification helpers must not load the service's config or credentials.
		if flags.ShowWarning {
			ShowToast()
			return nil
		}
		loadFromEnv() // 从环境变量加载配置，覆盖解析
		if flags.ConfigFile != "" {
			bytes, err := os.ReadFile(flags.ConfigFile)
			if err != nil {
				return fmt.Errorf("failed to read config file: %w", err)
			}
			err = json.Unmarshal(bytes, flags)
			if err != nil {
				return fmt.Errorf("failed to parse config file: %w", err)
			}
		}
		// After both of the above, so the precedence is explicit: -t / AGENT_TOKEN
		// win, then --token-file / AGENT_TOKEN_FILE, then the config file's token.
		if err := resolveToken(); err != nil {
			return err
		}
		tokenOnCommandLine = os.Getenv("AGENT_TOKEN") == "" && tokenFlagProvided(os.Args)
		if flags.PreferIPVersion != "" && flags.PreferIPVersion != "4" && flags.PreferIPVersion != "6" {
			return fmt.Errorf("invalid --prefer-ip-version value %q: expected 4 or 6", flags.PreferIPVersion)
		}
		// 捕获中止信号，优雅退出
		stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		stopWarning := func() {}
		if !flags.DisableWebSsh {
			stopWarning = startSecurityWarning(stopCtx)
		}
		defer stopWarning()
		warnIfTokenOnCommandLine()
		warnIfTokenFlagDeprecated()
		go func() {
			<-stopCtx.Done()
			log.Printf("shutting down gracefully...")
			stopWarning()
			netstatic.Stop()
			os.Exit(0)
		}()

		if flags.MonthRotate != 0 {
			err := netstatic.StartOrContinue()
			if err != nil {
				log.Println("Failed to start netstatic monitoring:", err)
			}
			nics, err := monitoring.InterfaceList()
			if err != nil {
				log.Println("Failed to get interface list for netstatic:", err)
			}
			err = netstatic.SetNewConfig(netstatic.NetStaticConfig{
				Nics: nics,
				// 周期重置日必须下发给 netstatic，而不是只在读取时传日期。
				//
				// 读取时传日期（原先的 `GetTotalTrafficBetween(重置日, 现在)`）意味着周期累计是
				// "对账本里还在的样本求和"，于是保留期一过累计值就会变小 —— 而下游把"变小"解读为
				// 计数器重置，把整个累计当成一次增量记下来（现场：41 GB 的单点幻影）。
				// 采样路径知道重置日之后，累计就与样本是否还在账本里无关。
				MonthRotate: flags.MonthRotate,
			})
			if err != nil {
				log.Println("Failed to set netstatic config:", err)
			}
		}

		// 允许用 --update-repo 覆盖自动更新源（默认已是本 fork 的仓库）
		if repo := strings.TrimSpace(flags.UpdateRepo); repo != "" {
			update.Repo = repo
		}
		log.Println("Nekomari Agent", update.CurrentVersion)
		log.Println("Github Repo:", update.Repo)

		// 设置 DNS 解析行为
		if flags.CustomDNS != "" {
			dnsresolver.SetCustomDNSServer(flags.CustomDNS)
			log.Printf("Using custom DNS server: %s", flags.CustomDNS)
		} else {
			// 未设置则使用系统默认 DNS（不使用内置列表）
			log.Printf("Using system default DNS resolver")
		}

		// Auto discovery
		if flags.AutoDiscoveryKey != "" {
			err := handleAutoDiscovery()
			if err != nil {
				return fmt.Errorf("auto-discovery failed: %w", err)
			}
		}
		diskList, err := monitoring.DiskList()
		if err != nil {
			log.Println("Failed to get disk list:", err)
		}
		log.Println("Monitoring Mountpoints:", diskList)
		interfaceList, err := monitoring.InterfaceList()
		if err != nil {
			log.Println("Failed to get interface list:", err)
		}
		log.Println("Monitoring Interfaces:", interfaceList)
		// The panel shows this too; saying it here means an operator reading the
		// journal and an operator reading the panel see the same answer.
		log.Println(server.ICMPCapabilityLogLine())

		// 忽略不安全的证书
		if flags.IgnoreUnsafeCert {
			http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
		// 自动更新
		if !flags.DisableAutoUpdate {
			err := update.CheckAndUpdate()
			if err != nil {
				log.Println("[ERROR]", err)
			}
			go update.DoUpdateWorks()
		}
		go server.DoUploadBasicInfoWorks()
		// 解锁探测要真的出网打 Netflix / YouTube / Cloudflare，所以单独一条慢循环，
		// 不跟着 basicInfo 的间隔走。
		go server.DoUploadUnlockWorks()
		for {
			server.UpdateBasicInfo()
			server.EstablishWebSocketConnection()
		}
	},
}

func Execute() {
	for i, arg := range os.Args {
		if arg == "-autoUpdate" || arg == "--autoUpdate" {
			log.Println("WARNING: The -autoUpdate flag is deprecated in version 0.0.9 and later. Use --disable-auto-update to configure auto-update behavior.")
			// 从参数列表中移除该参数，防止cobra解析错误
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
			break
		}
		if arg == "-memory-mode-available" || arg == "--memory-mode-available" {
			//flags.MemoryIncludeCache = true
			log.Println("WARNING: The --memory-mode-available flag is deprecated in version 1.0.70 and later. Use --memory-include-cache to report memory usage including cache/buffer.")
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
		}
	}

	if err := RootCmd.Execute(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func init() {
	RootCmd.PersistentFlags().StringVarP(&flags.Token, "token", "t", "", "API token")
	RootCmd.PersistentFlags().StringVar(&flags.TokenFile, "token-file", "",
		"Read the API token from a file (mode 0600) instead of -t, so it stays out of ps and systemctl show")
	//RootCmd.MarkPersistentFlagRequired("token")
	RootCmd.PersistentFlags().StringVarP(&flags.Endpoint, "endpoint", "e", "", "API endpoint")
	//RootCmd.MarkPersistentFlagRequired("endpoint")
	RootCmd.PersistentFlags().StringVar(&flags.AutoDiscoveryKey, "auto-discovery", "", "Auto discovery key for the agent")
	RootCmd.PersistentFlags().StringVar(&flags.BackupStatusFile, "backup-status-file", "",
		"Path to a JSON backup-status file; when set, the agent reports backup.age_seconds and backup.ok")
	RootCmd.PersistentFlags().StringVar(&flags.UpdateRepo, "update-repo", "",
		"GitHub repo (owner/name) used for auto-update; defaults to this fork's repository")
	RootCmd.PersistentFlags().BoolVar(&flags.DisableAutoUpdate, "disable-auto-update", false, "Disable automatic updates")
	RootCmd.PersistentFlags().BoolVar(&flags.DisableWebSsh, "disable-web-ssh", false, "Disable remote control(web ssh and rce)")
	//RootCmd.PersistentFlags().BoolVar(&flags.MemoryModeAvailable, "memory-mode-available", false, "[deprecated]Report memory as available instead of used.")
	RootCmd.PersistentFlags().Float64VarP(&flags.Interval, "interval", "i", 3.0, "Interval in seconds")
	RootCmd.PersistentFlags().BoolVarP(&flags.IgnoreUnsafeCert, "ignore-unsafe-cert", "u", false, "Ignore unsafe certificate errors")
	RootCmd.PersistentFlags().IntVarP(&flags.MaxRetries, "max-retries", "r", 3, "Maximum number of retries")
	RootCmd.PersistentFlags().IntVarP(&flags.ReconnectInterval, "reconnect-interval", "c", 5, "Reconnect interval in seconds")
	RootCmd.PersistentFlags().IntVar(&flags.InfoReportInterval, "info-report-interval", 5, "Interval in minutes for reporting basic info")
	RootCmd.PersistentFlags().StringVar(&flags.IncludeNics, "include-nics", "", "Comma-separated list of network interfaces to include")
	RootCmd.PersistentFlags().StringVar(&flags.ExcludeNics, "exclude-nics", "", "Comma-separated list of network interfaces to exclude")
	RootCmd.PersistentFlags().StringVar(&flags.IncludeMountpoints, "include-mountpoint", "", "Semicolon-separated list of mount points to include for disk statistics")
	RootCmd.PersistentFlags().IntVar(&flags.MonthRotate, "month-rotate", 0, "Month reset for network statistics (0 to disable)")
	RootCmd.PersistentFlags().BoolVar(&flags.MemoryIncludeCache, "memory-include-cache", false, "Include cache/buffer in memory usage")
	RootCmd.PersistentFlags().BoolVar(&flags.MemoryReportRawUsed, "memory-exclude-bcf", false, "Use \"raminfo.Used = v.Total - v.Free - v.Buffers - v.Cached\" calculation for memory usage")
	RootCmd.PersistentFlags().StringVar(&flags.CustomDNS, "custom-dns", "", "Custom DNS server to use (e.g. 8.8.8.8, 114.114.114.114). By default, the program uses the system DNS resolver.")
	RootCmd.PersistentFlags().BoolVar(&flags.EnableGPU, "gpu", false, "Enable detailed GPU monitoring (usage, memory, multi-GPU support)")
	RootCmd.PersistentFlags().BoolVar(&flags.ShowWarning, "show-warning", false, "Show security warning on Windows, run once as a subprocess")
	RootCmd.PersistentFlags().StringVar(&warningPanelHost, "warning-panel-host", "", "Panel host shown by the notification helper")
	RootCmd.PersistentFlags().StringVar(&warningRunAsUser, "warning-run-as-user", "", "Agent account shown by the notification helper")
	_ = RootCmd.PersistentFlags().MarkHidden("warning-panel-host")
	_ = RootCmd.PersistentFlags().MarkHidden("warning-run-as-user")
	RootCmd.PersistentFlags().StringVar(&flags.CustomIpv4, "custom-ipv4", "", "Custom IPv4 address to use")
	RootCmd.PersistentFlags().StringVar(&flags.CustomIpv6, "custom-ipv6", "", "Custom IPv6 address to use")
	RootCmd.PersistentFlags().BoolVar(&flags.GetIpAddrFromNic, "get-ip-addr-from-nic", false, "Get IP address from network interface")
	RootCmd.PersistentFlags().StringVar(&flags.ConfigFile, "config", "", "Path to the configuration file")
	RootCmd.PersistentFlags().BoolVar(&flags.DisableCompression, "disable-compression", false, "Disable v2 gzip/permessage-deflate compression")
	RootCmd.PersistentFlags().StringVar(&flags.PreferIPVersion, "prefer-ip-version", "", "Prefer IP version for dashboard connections: 4 or 6")
	RootCmd.PersistentFlags().ParseErrorsWhitelist.UnknownFlags = true
}

func loadFromEnv() {
	val := reflect.ValueOf(flags).Elem()
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// Get the env tag
		envTag := fieldType.Tag.Get("env")
		if envTag == "" {
			continue
		}

		// Get the environment variable value
		envValue := os.Getenv(envTag)
		if envValue == "" {
			continue
		}

		// Set the field based on its type
		switch field.Kind() {
		case reflect.String:
			field.SetString(envValue)
		case reflect.Bool:
			if strings.ToLower(envValue) == "true" || envValue == "1" {
				field.SetBool(true)
			}
		case reflect.Int:
			if intVal, err := strconv.Atoi(envValue); err == nil {
				field.SetInt(int64(intVal))
			}
		case reflect.Float64:
			if floatVal, err := strconv.ParseFloat(envValue, 64); err == nil {
				field.SetFloat(floatVal)
			}
		}
	}
}
