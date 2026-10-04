package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
	"unicode/utf16"
)

//go:embed windows.ps1
var windowsScript string

type Request struct {
	Action     string       `json:"action"`
	Config     Config       `json:"config"`
	Password   string       `json:"password,omitempty"`
	User       *UserContext `json:"user,omitempty"`
	Directory  string       `json:"directory,omitempty"`
	Executable string       `json:"executable,omitempty"`
	ConfigPath string       `json:"config_path,omitempty"`
	Scheduled  bool         `json:"scheduled"`
}

type Snapshot struct {
	Computer           string      `json:"computer"`
	OS                 string      `json:"os"`
	Build              string      `json:"build"`
	IsServer           bool        `json:"is_server"`
	IsAdmin            bool        `json:"is_admin"`
	IsDomainController bool        `json:"is_domain_controller"`
	Port               int         `json:"port"`
	RDPEnabled         bool        `json:"rdp_enabled"`
	NLA                bool        `json:"nla"`
	TimeZone           string      `json:"time_zone"`
	Languages          []string    `json:"languages"`
	UserName           string      `json:"user_name"`
	User               UserContext `json:"user"`
	TaskState          string      `json:"task_state"`
}

func encodedScript(script string) string {
	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}

/** @brief 固定脚本经标准输入接收 JSON，密码和配置值均不进入进程命令行。 */
func runWindows(req Request, output io.Writer) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("此程序仅支持 Windows")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	/** @brief 固定入口设置编码并读取嵌入脚本，控制命令行长度。 */
	bootstrap := "$ErrorActionPreference='Stop'; $global:ProgressPreference='SilentlyContinue'; [Console]::InputEncoding=New-Object System.Text.UTF8Encoding($false); [Console]::OutputEncoding=New-Object System.Text.UTF8Encoding($false); $e=[Console]::In.ReadToEnd() | ConvertFrom-Json; $p=$e.request; & ([scriptblock]::Create($e.script))"
	envelope, err := json.Marshal(struct {
		Script  string  `json:"script"`
		Request Request `json:"request"`
	}{windowsScript, req})
	if err != nil {
		return err
	}
	defer clear(envelope)
	cmd := exec.CommandContext(ctx, path, "-NoLogo", "-NoProfile", "-NonInteractive", "-OutputFormat", "Text", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodedScript(bootstrap))
	cmd.Stdin = bytes.NewReader(envelope)
	cmd.Stdout = output
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			_, _ = io.Copy(output, &stderr)
		}
		if ctx.Err() != nil {
			return fmt.Errorf("执行超时，请检查 Windows 中仍在执行的安装任务")
		}
		return fmt.Errorf("Windows 操作失败：%w", err)
	}
	return nil
}

func getSnapshot() (Snapshot, error) {
	var output bytes.Buffer
	var state Snapshot
	if err := runWindows(Request{Action: "status"}, &output); err != nil {
		return state, fmt.Errorf("%w\n%s", err, output.String())
	}
	if err := json.Unmarshal(bytes.TrimPrefix(output.Bytes(), []byte{0xef, 0xbb, 0xbf}), &state); err != nil {
		return state, fmt.Errorf("解析系统状态：%w", err)
	}
	return state, nil
}
