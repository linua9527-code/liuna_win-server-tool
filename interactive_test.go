package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

/** @brief 使用记录型执行器验证交互流程，避免测试修改本机系统。 */
func recordedApplication(input string) (*Application, *bytes.Buffer, *[]Request) {
	output := &bytes.Buffer{}
	requests := &[]Request{}
	app := newApplication(strings.NewReader(input), output)
	app.Run = func(req Request, _ io.Writer) error { *requests = append(*requests, req); return nil }
	app.Snapshot = func() (Snapshot, error) { return Snapshot{OS: "Test Windows Server"}, nil }
	app.ClearCredentials = func() (int, error) { return 0, nil }
	return app, output, requests
}

func TestNoArgsOpensMenuWithoutChanges(t *testing.T) {
	directory := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Error(err)
		}
	})
	app, output, requests := recordedApplication("0\n")
	if err := app.dispatch(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "一键配置并清理") {
		t.Fatal("未显示交互菜单")
	}
	if len(*requests) != 0 {
		t.Fatal("打开菜单执行了系统变更")
	}
	if _, err := os.Stat(defaultConfigName); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("退出菜单创建了配置文件")
	}
}

func TestInteractiveEditAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	app, _, requests := recordedApplication("4\n1\ny\n3390\ny\n192.0.2.1,2001:db8::/32\ny\n8\n20\n10\n0\n9\n\n0\n")
	if err := app.dispatch([]string{"interactive", "--config", path}); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.RDP.Port != 3390 || config.RDP.Threshold != 8 || config.RDP.DurationMinutes != 20 || config.RDP.ResetMinutes != 10 {
		t.Fatalf("保存了错误的选项：%+v", config.RDP)
	}
	if !reflect.DeepEqual(config.RDP.RemoteAddresses, []string{"192.0.2.1", "2001:db8::/32"}) {
		t.Fatal("来源地址丢失")
	}
	if len(*requests) != 0 {
		t.Fatal("编辑或保存配置执行了系统变更")
	}
}

func TestCanceledTaskRemovalDoesNotExecute(t *testing.T) {
	for _, input := range []string{"5\n3\nn\n0\n0\n"} {
		app, _, requests := recordedApplication(input)
		if err := app.interactive(filepath.Join(t.TempDir(), "config.json")); err != nil {
			t.Fatal(err)
		}
		if len(*requests) != 0 {
			t.Fatalf("取消操作后仍执行：%v", *requests)
		}
	}
}

func TestSetupExecutesAllCleanupWithoutExtraPrompts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config := defaultConfig()
	config.Cleanup.EventLogs = true
	if err := writeJSON(path, config); err != nil {
		t.Fatal(err)
	}
	app, output, requests := recordedApplication("1\n0\n")
	if err := app.interactive(path); err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, req := range *requests {
		actions = append(actions, req.Action)
	}
	if !reflect.DeepEqual(actions, []string{"preflight", "apply", "validate-target", "clean"}) {
		t.Fatalf("一键执行顺序错误：%v", actions)
	}
	if strings.Contains(output.String(), "确认") || strings.Contains(output.String(), "执行以上选项") {
		t.Fatal("一键执行仍需额外确认")
	}
	if (*requests)[3].Config.Cleanup != defaultConfig().Cleanup {
		t.Fatal("一键执行未包含全部清理项")
	}
}

func TestSharedPasswordReader(t *testing.T) {
	config := defaultConfig()
	config.Password.Change = true
	path := filepath.Join(t.TempDir(), "config.json")
	if err := writeJSON(path, config); err != nil {
		t.Fatal(err)
	}
	app, _, requests := recordedApplication("2\nTest-Password-123\nTest-Password-123\n0\n")
	app.Console.Secret = func(reader *bufio.Reader, _ io.Writer, _ string) (string, error) {
		value, err := reader.ReadString('\n')
		return strings.TrimRight(value, "\r\n"), err
	}
	if err := app.interactive(path); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 2 || (*requests)[1].Password != "Test-Password-123" {
		t.Fatalf("菜单缓冲区导致密码输入丢失：%v", *requests)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Test-Password") {
		t.Fatal("密码写入了配置文件")
	}
}

func TestEditInterruptionKeepsPreviousConfiguration(t *testing.T) {
	for _, input := range []string{"y\n3390\ny\n", "y\n3390\ny\ninvalid-address\ny\n8\n20\n10\n"} {
		session := interactiveSession{Config: defaultConfig()}
		original := session.Config
		app, _, _ := recordedApplication(input)
		if err := app.editSection("1", &session); err == nil {
			t.Fatal("接受中断或无效编辑")
		}
		if !reflect.DeepEqual(session.Config, original) || session.Dirty {
			t.Fatal("失败的编辑更改了已有配置")
		}
	}
}

func TestInteractiveReturnsToMenuAfterFailure(t *testing.T) {
	app, output, requests := recordedApplication("2\n0\n")
	app.Run = func(req Request, _ io.Writer) error {
		*requests = append(*requests, req)
		return fmt.Errorf("预检测试失败")
	}
	if err := app.interactive(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 1 || (*requests)[0].Action != "preflight" {
		t.Fatal("预检失败后继续配置")
	}
	if !strings.Contains(output.String(), "预检测试失败") {
		t.Fatal("失败原因未展示")
	}
}

func TestFlagsAndScheduledCleanupStillWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config := defaultConfig()
	if err := writeJSON(path, config); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"-config", "--config"} {
		app, _, requests := recordedApplication("")
		if err := app.dispatch([]string{"preflight", flag, path}); err != nil {
			t.Fatal(err)
		}
		if len(*requests) != 1 || (*requests)[0].Action != "preflight" {
			t.Fatal("命令行预检未执行")
		}
	}
	app, _, requests := recordedApplication("")
	if err := app.dispatch([]string{"clean", "--config", path, "--scheduled", "--user-sid", "S-1-5-21-1-2-3-1000", "--user-profile", `C:\Users\test`}); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 2 || !(*requests)[1].Scheduled || (*requests)[1].User.SID != "S-1-5-21-1-2-3-1000" {
		t.Fatal("计划任务入口丢失用户信息")
	}
}

func TestCleanupContinuesToCredentialsAfterChannelFailure(t *testing.T) {
	app, _, _ := recordedApplication("")
	credentialCalled := false
	channelErr := errors.New("通道清理失败")
	credentialErr := errors.New("凭据清理失败")
	app.Run = func(req Request, _ io.Writer) error {
		if req.Action == "clean" {
			return channelErr
		}
		return nil
	}
	app.ClearCredentials = func() (int, error) { credentialCalled = true; return 0, credentialErr }
	err := app.clean(Request{Action: "clean", Config: defaultConfig()})
	if !credentialCalled || !errors.Is(err, channelErr) || !errors.Is(err, credentialErr) {
		t.Fatalf("清理提前停止或错误信息丢失：%v", err)
	}
	credentialCalled = false
	app.Run = func(Request, io.Writer) error { return errors.New("权限预检失败") }
	if err := app.clean(Request{Action: "clean", Config: defaultConfig()}); err == nil || credentialCalled {
		t.Fatal("预检失败后仍执行凭据清理")
	}
}

func TestCleanCommandWithoutConfigUsesAllDefaults(t *testing.T) {
	directory := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Error(err)
		}
	})
	app, _, requests := recordedApplication("")
	if err := app.dispatch([]string{"clean"}); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 2 || (*requests)[1].Config.Cleanup != defaultConfig().Cleanup {
		t.Fatal("默认清理未执行全部项目")
	}
	*requests = nil
	if err := app.dispatch([]string{"clean", "--config", filepath.Join(directory, "missing.json")}); err == nil || len(*requests) != 0 {
		t.Fatal("显式指定的配置缺失时仍进行了清理")
	}
}

func TestInteractiveEOF(t *testing.T) {
	app, _, requests := recordedApplication("")
	if err := app.interactive(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("输入结束处理异常：%v", err)
	}
	if len(*requests) != 0 {
		t.Fatal("输入结束后执行了系统操作")
	}
}
