package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/Aone2233/nekomari/agent/dnsresolver"
	"github.com/Aone2233/nekomari/agent/utils"
)

// AutoDiscoveryConfig 自动发现配置结构体
type AutoDiscoveryConfig struct {
	UUID  string `json:"uuid"`
	Token string `json:"token"`
}

// RegisterRequest 注册请求结构体
type RegisterRequest struct {
	Key string `json:"key"`
}

// RegisterResponse 注册响应结构体
type RegisterResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		UUID  string `json:"uuid"`
		Token string `json:"token"`
	} `json:"data"`
}

// getAutoDiscoveryFilePath 获取自动发现配置文件路径
func getAutoDiscoveryFilePath() string {
	// 获取程序运行目录
	execPath, err := os.Executable()
	if err != nil {
		log.Println("Failed to get executable path:", err)
		return "auto-discovery.json"
	}
	execDir := filepath.Dir(execPath)
	return filepath.Join(execDir, "auto-discovery.json")
}

// autoDiscoveryFilePath 是身份文件路径的取用点。
//
// 抽成变量是为了让测试能把文件指到临时目录：这个文件里有节点 token，是整包里最
// 敏感的东西，而它此前没有任何测试。
var autoDiscoveryFilePath = getAutoDiscoveryFilePath

// tightenIdentityFilePermissions 去掉身份文件的组/其他读权限。
//
// 为什么需要它：saveAutoDiscoveryConfig 曾经用 0644 写这个文件（里面有 uuid 与
// token），而同一个 agent 在读取 --token-file 时**会拒绝**组/其他可读的文件
// （见 token.go 的 tokenFileReadableByOthers）。写出去的东西至少要和读进来的东西
// 一样严格。已存在的旧安装不会自动重新注册，所以读取时顺手就地收紧一次。
func tightenIdentityFilePermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	log.Printf("Warning: %s is readable by group/other (mode %#o); tightening to 0600 because it holds this node's token",
		path, info.Mode().Perm())
	return os.Chmod(path, 0o600)
}

// loadAutoDiscoveryConfig 加载自动发现配置
func loadAutoDiscoveryConfig() (*AutoDiscoveryConfig, error) {
	configPath := autoDiscoveryFilePath()

	// 检查文件是否存在
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, nil // 文件不存在，返回nil
	}

	// 收紧历史版本留下的宽权限（见 tightenIdentityFilePermissions 的说明）。
	if err := tightenIdentityFilePermissions(configPath); err != nil {
		log.Printf("Warning: could not tighten permissions on %s: %v", configPath, err)
	}

	// 读取文件内容
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read auto-discovery config: %v", err)
	}

	// 解析JSON
	var config AutoDiscoveryConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse auto-discovery config: %v", err)
	}

	return &config, nil
}

// saveAutoDiscoveryConfig 保存自动发现配置
func saveAutoDiscoveryConfig(config *AutoDiscoveryConfig) error {
	configPath := autoDiscoveryFilePath()

	// 序列化为JSON
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal auto-discovery config: %v", err)
	}

	// 写入文件。
	//
	// 必须是 0600：这里面存的是这个节点的 token，而 0644 会让本机任何用户都能读到
	// 并冒充该节点。现场实测某台节点的这个文件就是 0644（2026-10-01）。
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write auto-discovery config: %v", err)
	}

	log.Printf("Auto-discovery config saved to: %s", configPath)
	return nil
}

// registerWithAutoDiscovery 使用自动发现key注册
func registerWithAutoDiscovery() error {
	// 构造注册请求
	requestData := RegisterRequest{
		Key: flags.AutoDiscoveryKey,
	}

	hostname, _ := os.Hostname()

	jsonData, err := json.Marshal(requestData)
	if err != nil {
		return fmt.Errorf("failed to marshal register request: %v", err)
	}

	// 构造请求URL
	endpoint := flags.Endpoint
	if len(endpoint) > 0 && endpoint[len(endpoint)-1] == '/' {
		endpoint = endpoint[:len(endpoint)-1]
	}

	// 转换中文域名为 ASCII 兼容编码
	endpoint, err = utils.ConvertIDNToASCII(endpoint)
	if err != nil {
		log.Printf("Warning: Failed to convert IDN to ASCII: %v", err)
		// 继续使用原始 endpoint，可能在某些情况下仍能工作
	}

	registerURL := fmt.Sprintf("%s/api/clients/register?name=%s", endpoint, url.QueryEscape(hostname))

	// 创建HTTP请求
	req, err := http.NewRequest("POST", registerURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create register request: %v", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", flags.AutoDiscoveryKey))

	// 发送请求
	client := dnsresolver.GetHTTPClientWithPreference(30*time.Second, flags.PreferIPVersion)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send register request: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// 解析响应
	var registerResp RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&registerResp); err != nil {
		return fmt.Errorf("failed to parse register response: %v", err)
	}

	// 检查响应状态
	if registerResp.Status != "success" {
		return fmt.Errorf("register request failed: %s", registerResp.Message)
	}

	// 保存配置
	config := &AutoDiscoveryConfig{
		UUID:  registerResp.Data.UUID,
		Token: registerResp.Data.Token,
	}

	if err := saveAutoDiscoveryConfig(config); err != nil {
		return fmt.Errorf("failed to save auto-discovery config: %v", err)
	}

	// 设置token
	flags.Token = registerResp.Data.Token
	log.Printf("Successfully registered with auto-discovery. UUID: %s", registerResp.Data.UUID)

	return nil
}

// handleAutoDiscovery 处理自动发现逻辑
func handleAutoDiscovery() error {
	// 尝试加载现有配置
	config, err := loadAutoDiscoveryConfig()
	if err != nil {
		log.Printf("Failed to load auto-discovery config: %v", err)
		// 继续尝试注册
	}

	if config != nil {
		// 配置文件存在，使用现有token
		flags.Token = config.Token
		log.Printf("Using existing auto-discovery token for UUID: %s", config.UUID)
		return nil
	}

	// 配置文件不存在，进行注册
	log.Println("Auto-discovery config not found, registering with server...")
	return registerWithAutoDiscovery()
}
