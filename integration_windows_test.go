//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestPowerShellSyntax(t *testing.T) {
	/** @brief 使用服务器实际自带的 Windows PowerShell 5.1 解析嵌入脚本。 */
	script := "[Console]::InputEncoding=New-Object System.Text.UTF8Encoding($false); $tokens=$null; $errors=$null; [System.Management.Automation.Language.Parser]::ParseInput([Console]::In.ReadToEnd(),[ref]$tokens,[ref]$errors) | Out-Null; if($errors.Count -gt 0){$errors | Out-String | Write-Output; exit 1}"
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.Command(path, "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedScript(script))
	reader, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() { defer reader.Close(); reader.Write([]byte(windowsScript)) }()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("脚本解析失败：%v\n%s", err, output)
	}
}

func TestReadOnlyStatus(t *testing.T) {
	state, err := getSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if state.Build == "" || state.OS == "" || state.User.SID == "" {
		t.Fatalf("系统状态不完整：%+v", state)
	}
}

func TestWindowsPreflightWithoutChanges(t *testing.T) {
	state, err := getSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	config := defaultConfig()
	config.RDP.Enabled = false
	config.RDP.Lockout = false
	config.Language.Enabled = false
	config.TimeZone = ""
	var output bytes.Buffer
	err = runWindows(Request{Action: "preflight", Config: config}, &output)
	if state.IsAdmin {
		if err != nil || !strings.Contains(output.String(), "预检通过") {
			t.Fatalf("Windows 预检失败：%v %s", err, output.String())
		}
	} else if err == nil || !strings.Contains(output.String(), "管理员") {
		t.Fatalf("管理员权限预检异常：%v %s", err, output.String())
	}
	if strings.Contains(output.String(), "CLIXML") {
		t.Fatal("系统错误输出混入 PowerShell 进度序列化数据")
	}
}

/** @brief 只提取待验证函数并在临时目录中执行，不执行产品入口或系统清理。 */
func runPowerShellFixture(t *testing.T, functions []string, body string, root string) {
	t.Helper()
	payload, err := json.Marshal(struct {
		Source    string   `json:"source"`
		Functions []string `json:"functions"`
		Body      string   `json:"body"`
		Root      string   `json:"root"`
	}{windowsScript, functions, body, root})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := "$ErrorActionPreference='Stop'; $global:ProgressPreference='SilentlyContinue'; [Console]::InputEncoding=New-Object System.Text.UTF8Encoding($false); [Console]::OutputEncoding=New-Object System.Text.UTF8Encoding($false); $fixture=[Console]::In.ReadToEnd() | ConvertFrom-Json; $tokens=$null; $errors=$null; $ast=[System.Management.Automation.Language.Parser]::ParseInput($fixture.source,[ref]$tokens,[ref]$errors); if($errors.Count){throw '脚本解析失败'}; foreach($name in $fixture.functions){$function=$ast.Find({param($node) $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name},$true); if(-not $function){throw ('缺少函数：'+$name)}; . ([scriptblock]::Create($function.Extent.Text))}; & ([scriptblock]::Create($fixture.body))"
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.Command(path, "-NoProfile", "-NonInteractive", "-OutputFormat", "Text", "-EncodedCommand", encodedScript(bootstrap))
	cmd.Stdin = bytes.NewReader(payload)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Windows 场景验证失败：%v\n%s", err, output)
	}
}

func TestSupportedWindowsVersions(t *testing.T) {
	runPowerShellFixture(t, []string{"Assert-WindowsVersion"}, `
$cases = @(
    @{version='10.0.10240'; build=10240; server=$false; supported=$true},
    @{version='10.0.19045'; build=19045; server=$false; supported=$true},
    @{version='10.0.22000'; build=22000; server=$false; supported=$true},
    @{version='10.0.26100'; build=26100; server=$false; supported=$true},
    @{version='10.0.14393'; build=14393; server=$true; supported=$true},
    @{version='10.0.26100'; build=26100; server=$true; supported=$true},
    @{version='6.3.9600'; build=9600; server=$false; supported=$false},
    @{version='6.3.9600'; build=9600; server=$true; supported=$false},
    @{version='10.0.10240'; build=10240; server=$true; supported=$false}
)
foreach ($case in $cases) {
    $accepted = $true
    try { Assert-WindowsVersion ([pscustomobject]@{os_version=$case.version; build=$case.build; is_server=$case.server}) }
    catch { $accepted = $false }
    if ($accepted -ne $case.supported) { throw ('Windows 版本判断错误：'+$case.version+', server='+$case.server) }
}
`, "")
}

func TestAllTempFilesAndEmptyDirectoriesCleanup(t *testing.T) {
	runPowerShellFixture(t, []string{"Assert-NoReparse", "Clear-TempDirectory"}, `
$temp = Join-Path $fixture.root 'temp'
$outside = Join-Path $fixture.root 'outside'
$null = New-Item -ItemType Directory -Path $temp, $outside
$nested = Join-Path $temp 'nested\child'
$null = New-Item -ItemType Directory -Path $nested -Force
$null = New-Item -ItemType Directory -Path (Join-Path $temp 'empty')
$fresh = Join-Path $temp 'fresh.tmp'
$old = Join-Path $temp 'old.tmp'
$readonly = Join-Path $nested 'readonly.tmp'
$locked = Join-Path $temp 'locked.tmp'
$external = Join-Path $outside 'keep.txt'
foreach ($path in @($fresh, $old, $readonly, $locked, $external)) { [IO.File]::WriteAllText($path, 'fixture') }
[IO.File]::SetLastWriteTimeUtc($old, [DateTime]::UtcNow.AddDays(-30))
[IO.File]::SetAttributes($readonly, [IO.FileAttributes]::ReadOnly)
$junction = Join-Path $temp 'junction'
$null = New-Item -ItemType Junction -Path $junction -Target $outside
$stream = [IO.File]::Open($locked, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
try {
    Clear-TempDirectory $temp
    foreach ($path in @($fresh, $old, $readonly, (Join-Path $temp 'empty'), (Join-Path $temp 'nested'))) {
        if (Test-Path -LiteralPath $path) { throw ('可清理对象仍存在：'+$path) }
    }
    foreach ($path in @($temp, $locked, $external, $junction)) {
        if (-not (Test-Path -LiteralPath $path)) { throw ('应保留对象被删除：'+$path) }
    }
} finally { $stream.Dispose() }
Clear-TempDirectory $temp
if (Test-Path -LiteralPath $locked) { throw '解除占用后临时文件仍存在。' }
$failed = $false
try { Clear-TempDirectory $junction } catch { $failed = $true }
if (-not $failed -or -not (Test-Path -LiteralPath $external)) { throw '清理入口的链接路径防护异常。' }
`, t.TempDir())
}

func TestEventLogFailureStillAttemptsRemainingChannels(t *testing.T) {
	runPowerShellFixture(t, []string{"Invoke-EventLogClear", "Clear-EventLogs"}, `
$fakeTool = Join-Path $fixture.root 'eventlog-fixture.ps1'
$source = @'
param([string]$Action, [string]$Channel)
if ($Action -eq 'el') {
    'First', 'Restricted', 'Last'
    $global:LASTEXITCODE = 0
} elseif ($Action -eq 'cl') {
    Add-Content -LiteralPath (Join-Path $PSScriptRoot 'attempts.txt') -Value $Channel
    if ($Channel -eq 'Restricted') {
        Write-Error 'fixture access denied'
        $global:LASTEXITCODE = 5
    } else { $global:LASTEXITCODE = 0 }
}
'@
[IO.File]::WriteAllText($fakeTool, $source, (New-Object Text.UTF8Encoding($false)))
$failed = $false
try { Clear-EventLogs $fakeTool }
catch {
    $failed = $true
    if ($_.Exception.Message -notmatch 'Restricted' -or $_.Exception.Message -notmatch 'access denied') {
        throw '事件日志失败原因未汇总。'
    }
}
if (-not $failed) { throw '日志通道失败被报告为成功。' }
$attempts = @(Get-Content -LiteralPath (Join-Path $fixture.root 'attempts.txt'))
if (($attempts -join ',') -ne 'First,Restricted,Last') { throw '日志清理在某个通道失败后提前停止。' }
`, t.TempDir())
}

func TestUnicodeScriptEncoding(t *testing.T) {
	text := "中文😀"
	data, err := base64.StdEncoding.DecodeString(encodedScript(text))
	if err != nil {
		t.Fatal(err)
	}
	var units []uint16
	for i := 0; i < len(data); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(data[i:]))
	}
	if string(utf16.Decode(units)) != text {
		t.Fatal("UTF-16 编码损坏")
	}
}
