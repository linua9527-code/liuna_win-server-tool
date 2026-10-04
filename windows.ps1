$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$c = $p.config
$taskName = 'WinServerTool-Cleanup'
$credentialTaskName = 'WinServerTool-RDPCredentials'

function Get-State {
    $os = Get-CimInstance Win32_OperatingSystem
    $computer = Get-CimInstance Win32_ComputerSystem
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    $rdp = Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp'
    $terminal = Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Terminal Server'
    $task = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    $state = '未安装'
    if ($task) { $state = [string]$task.State }
    return [pscustomobject]@{
        computer = $env:COMPUTERNAME
        os = $os.Caption
        build = [string]$os.BuildNumber
        os_version = [string]$os.Version
        os_sku = [int]$os.OperatingSystemSKU
        is_server = ($os.ProductType -ne 1)
        is_admin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
        is_domain_controller = ($computer.DomainRole -ge 4)
        port = [int]$rdp.PortNumber
        rdp_enabled = ($terminal.fDenyTSConnections -eq 0)
        nla = ($rdp.UserAuthentication -eq 1)
        time_zone = (Get-TimeZone).Id
        languages = @($os.MUILanguages)
        user_name = $identity.Name
        user = @{ sid = $identity.User.Value; profile = $env:USERPROFILE }
        task_state = $state
    }
}

function Assert-WindowsVersion($State) {
    $version = [version]$State.os_version
    $minimumBuild = 10240
    if ($State.is_server) { $minimumBuild = 14393 }
    if ($version.Major -lt 10 -or [int]$State.build -lt $minimumBuild) {
        throw '支持 Windows 10、Windows 11 和 Windows Server 2016 及以上版本。'
    }
}

function Assert-Target {
    $state = Get-State
    Assert-WindowsVersion $state
    if (-not $state.is_admin) { throw '请在以管理员身份运行的终端中执行。' }
    return $state
}

function Invoke-Native([string]$Path, [string[]]$Arguments) {
    & $Path @Arguments | Out-Host
    if ($LASTEXITCODE -ne 0) { throw ('系统命令执行失败：{0}，退出码 {1}' -f $Path, $LASTEXITCODE) }
}

function Test-Configuration {
    $state = Assert-Target
    if ($state.is_domain_controller -and ($c.rdp.configure_lockout -or $c.password.change)) {
        throw '域控制器应使用域账户策略；请关闭本地账户锁定与本地密码修改。'
    }
    if ($c.time_zone) { $null = Get-TimeZone -Id $c.time_zone }
    if ($c.rdp.enabled) {
        if (-not $state.is_server -and $state.os_sku -in @(2, 3, 5, 11, 26, 47, 98, 99, 100, 101, 123)) {
            throw '此 Windows 版本未提供远程桌面主机功能。请关闭远程桌面配置后执行其他操作。'
        }
        $null = Get-Command New-NetFirewallRule
        if ($c.rdp.port -ne $state.port) {
            $tcp = @(Get-NetTCPConnection -LocalPort $c.rdp.port -State Listen -ErrorAction SilentlyContinue)
            $udp = @(Get-NetUDPEndpoint -LocalPort $c.rdp.port -ErrorAction SilentlyContinue)
            if ($tcp.Count -gt 0 -or $udp.Count -gt 0) { throw '新端口已被占用，请选择其他端口。' }
        }
        if ($state.nla -and -not $c.rdp.require_nla) { throw '当前已启用 NLA，请保留 require_nla=true。' }
    }
    if ($c.language.enabled) {
        $null = [Globalization.CultureInfo]::GetCultureInfo($c.language.tag)
        foreach ($command in @('Set-WinUserLanguageList', 'Set-WinUILanguageOverride', 'Set-WinSystemLocale', 'Set-Culture')) {
            $null = Get-Command $command
        }
        if ($c.language.copy_to_system) { $null = Get-Command Copy-UserInternationalSettingsToSystem }
        if ($state.languages -notcontains $c.language.tag) {
            if (-not $c.language.install_if_missing) {
                throw '缺少目标界面语言包。请预装语言包，或设置 install_if_missing=true 并提供匹配版本的 CAB 文件。'
            }
            if ($c.language.package_cab) {
                $package = Get-Item -LiteralPath $c.language.package_cab
                if ($package.PSIsContainer -or $package.Extension -ne '.cab') { throw '语言包路径应指向 CAB 文件。' }
                $null = Get-Command Add-WindowsPackage
            } else {
                if (-not (Get-Command Install-Language -ErrorAction SilentlyContinue)) {
                    throw '此系统未提供在线语言安装接口，请在 package_cab 中指定匹配系统版本的本机语言包。'
                }
            }
        }
    }
    if ($c.password.change) {
        $name = $c.password.username
        if (-not $name) { $name = $env:USERNAME }
        $account = Get-LocalUser -Name $name
        if (-not $account.Enabled) { throw '目标本地账户处于禁用状态。' }
        if ($account.PrincipalSource -and $account.PrincipalSource -ne 'Local') { throw '密码修改只接受本地账户。' }
        $null = Get-Command Set-LocalUser
    }
    Write-Output '预检通过。'
}

function New-RDPRule([string]$Protocol, [string]$Name) {
    $null = New-NetFirewallRule -Name $Name -DisplayName ('WinServerTool RDP ' + $Protocol) `
        -Direction Inbound -Action Allow -Protocol $Protocol -LocalPort $c.rdp.port `
        -RemoteAddress $c.rdp.remote_addresses -Profile Any -Enabled True
}

function Set-Lockout {
    $directory = Join-Path $env:TEMP ('wsi-policy-' + [Guid]::NewGuid().ToString('N'))
    $null = New-Item -ItemType Directory -Path $directory
    try {
        $inf = @"
[Unicode]
Unicode=yes
[Version]
signature="`$CHICAGO`$"
Revision=1
[System Access]
LockoutBadCount=$($c.rdp.lockout_threshold)
ResetLockoutCount=$($c.rdp.lockout_reset_minutes)
LockoutDuration=$($c.rdp.lockout_duration_minutes)
"@
        $path = Join-Path $directory 'policy.inf'
        [IO.File]::WriteAllText($path, $inf, [Text.Encoding]::Unicode)
        Invoke-Native (Join-Path $env:SystemRoot 'System32\secedit.exe') @('/configure', '/db', (Join-Path $directory 'policy.sdb'), '/cfg', $path, '/areas', 'SECURITYPOLICY', '/log', (Join-Path $directory 'policy.log'), '/quiet')
        Write-Output '已设置本地账户登录失败锁定策略。域策略和内置 Administrator 的锁定行为仍由系统策略决定。'
    } finally {
        Remove-Item -LiteralPath $directory -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Apply-Configuration {
    Test-Configuration
    if ($c.language.enabled) {
        $state = Get-State
        if ($state.languages -notcontains $c.language.tag) {
            Write-Output '正在安装语言包，请等待……'
            if ($c.language.package_cab) {
                $null = Add-WindowsPackage -Online -PackagePath $c.language.package_cab -NoRestart
            } else {
                $null = Install-Language -Language $c.language.tag
            }
            if ((Get-CimInstance Win32_OperatingSystem).MUILanguages -notcontains $c.language.tag) {
                throw '语言包尚未生效，请先完成系统要求的重启，再重新执行配置。'
            }
        }
        $list = New-WinUserLanguageList -Language $c.language.tag
        Set-WinUserLanguageList -LanguageList $list -Force
        Set-WinUILanguageOverride -Language $c.language.tag
        Set-Culture -CultureInfo $c.language.tag
        Set-WinSystemLocale -SystemLocale $c.language.tag
        if ($c.language.copy_to_system) {
            Copy-UserInternationalSettingsToSystem -WelcomeScreen $true -NewUser $true
        }
        Write-Output '已设置当前用户界面语言和系统区域语言；界面语言注销后生效，系统区域语言重启后生效。'
    }
    if ($c.time_zone) {
        Set-TimeZone -Id $c.time_zone
        Write-Output ('已设置时区：' + $c.time_zone)
    }
    if ($c.rdp.configure_lockout) { Set-Lockout }
    if ($c.password.change) {
        if (-not $p.password) { throw '请通过程序的隐藏输入提供新密码。' }
        $name = $c.password.username
        if (-not $name) { $name = $env:USERNAME }
        $secret = ConvertTo-SecureString -String $p.password -AsPlainText -Force
        try { Set-LocalUser -Name $name -Password $secret } finally { $secret.Dispose(); $p.password = $null }
        Write-Output ('已修改本地账户密码：' + $name)
    }
    if ($c.rdp.enabled) {
        $rdpPath = 'HKLM:\SYSTEM\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp'
        $terminalPath = 'HKLM:\SYSTEM\CurrentControlSet\Control\Terminal Server'
        $previous = Get-ItemProperty $rdpPath
        $oldPort = [int]$previous.PortNumber
        $oldDeny = (Get-ItemProperty $terminalPath).fDenyTSConnections
        $oldRules = @(Get-NetFirewallRule -PolicyStore PersistentStore -ErrorAction Stop | Where-Object { $_.Name -like 'WinServerTool-RDP-*' })
        $suffix = [Guid]::NewGuid().ToString('N')
        $tcpName = 'WinServerTool-RDP-TCP-' + $suffix
        $udpName = 'WinServerTool-RDP-UDP-' + $suffix
        try {
            New-RDPRule 'TCP' $tcpName
            New-RDPRule 'UDP' $udpName
            Set-ItemProperty $rdpPath -Name UserAuthentication -Type DWord -Value ([int][bool]$c.rdp.require_nla)
            Set-ItemProperty $terminalPath -Name fDenyTSConnections -Type DWord -Value 0
            Set-ItemProperty $rdpPath -Name PortNumber -Type DWord -Value $c.rdp.port
        } catch {
            $failure = $_.Exception.Message
            $rollbackErrors = New-Object 'System.Collections.Generic.List[string]'
            foreach ($name in @($tcpName, $udpName)) {
                try {
                    $rule = Get-NetFirewallRule -Name $name -ErrorAction SilentlyContinue
                    if ($rule) { $null = Remove-NetFirewallRule -Name $name }
                } catch { $rollbackErrors.Add($_.Exception.Message) }
            }
            try {
                Set-ItemProperty $rdpPath -Name PortNumber -Type DWord -Value $oldPort
                Set-ItemProperty $rdpPath -Name UserAuthentication -Type DWord -Value $previous.UserAuthentication
                Set-ItemProperty $terminalPath -Name fDenyTSConnections -Type DWord -Value $oldDeny
            } catch { $rollbackErrors.Add($_.Exception.Message) }
            if ($rollbackErrors.Count -gt 0) { throw ($failure + '; 回滚异常：' + ($rollbackErrors -join '; ')) }
            throw ($failure + '; 已恢复本次远程桌面与防火墙变更。')
        }
        foreach ($rule in $oldRules) {
            try { $null = Remove-NetFirewallRule -Name $rule.Name }
            catch { Write-Output ('旧的工具防火墙规则删除失败，请检查：' + $rule.Name) }
        }
        Write-Output ('已配置远程桌面端口：{0}；请先放行云平台安全组中的 TCP/UDP {0}。' -f $c.rdp.port)
        if ($oldPort -ne $c.rdp.port) {
            Write-Output ('当前连接保持原端口 {0}；新端口需重启后生效。程序保留系统原有防火墙规则。' -f $oldPort)
        }
    }
    Write-Output '配置阶段完成。程序未重启服务器。'
}

function Remove-HistoryKey($Root, [string]$Path) {
    $Root.DeleteSubKeyTree($Path, $false)
}

function Clear-UserRegistry([string]$SID, [string]$ProfilePath) {
    $mounted = $false
    $mount = $SID
    $root = [Microsoft.Win32.Registry]::Users.OpenSubKey($mount, $true)
    if (-not $root) {
        $hive = Join-Path $ProfilePath 'NTUSER.DAT'
        if (-not (Test-Path -LiteralPath $hive)) { throw '目标用户注册表文件缺失。' }
        $mount = 'WSI_' + [Guid]::NewGuid().ToString('N')
        Invoke-Native (Join-Path $env:SystemRoot 'System32\reg.exe') @('load', ('HKU\' + $mount), $hive)
        $mounted = $true
        $root = [Microsoft.Win32.Registry]::Users.OpenSubKey($mount, $true)
    }
    try {
        if (-not $root) { throw '打开用户注册表失败。' }
        if ($c.cleanup.rdp_history) {
            $key = $root.OpenSubKey('Software\Microsoft\Terminal Server Client\Default', $true)
            if ($key) {
                try {
                    foreach ($name in $key.GetValueNames()) {
                        if ($name -match '^MRU\d+$') { $key.DeleteValue($name, $false) }
                    }
                } finally { $key.Dispose() }
            }
            Remove-HistoryKey $root 'Software\Microsoft\Terminal Server Client\Servers'
        }
        if ($c.cleanup.recent_files) {
            foreach ($path in @('Software\Microsoft\Windows\CurrentVersion\Explorer\RecentDocs', 'Software\Microsoft\Windows\CurrentVersion\Explorer\RunMRU', 'Software\Microsoft\Windows\CurrentVersion\Explorer\TypedPaths')) {
                Remove-HistoryKey $root $path
            }
        }
    } finally {
        if ($root) { $root.Dispose() }
        if ($mounted) { Invoke-Native (Join-Path $env:SystemRoot 'System32\reg.exe') @('unload', ('HKU\' + $mount)) }
    }
}

function Assert-NoReparse([string]$Path) {
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
    while ($item) {
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw ('清理及任务目录应为普通本机路径：' + $Path) }
        $parent = Split-Path -Parent $item.FullName
        if (-not $parent -or $parent -eq $item.FullName) { break }
        $item = Get-Item -LiteralPath $parent -Force -ErrorAction SilentlyContinue
    }
}

function Remove-OrdinaryFile([string]$Path) {
    Assert-NoReparse $Path
    if (Test-Path -LiteralPath $Path -PathType Leaf) { Remove-Item -LiteralPath $Path -Force }
}

<#
@brief 全量清理临时文件并按子目录优先顺序移除空目录，保留清理根目录。
@param Path 要清理的临时目录；跳过重解析项，避免遍历链接指向的其他位置。
#>
function Clear-TempDirectory([string]$Path) {
    Assert-NoReparse $Path
    if (-not (Test-Path -LiteralPath $Path -PathType Container)) { return }
    $stack = New-Object 'System.Collections.Generic.Stack[string]'
    $directories = New-Object 'System.Collections.Generic.List[string]'
    $stack.Push($Path)
    $deleted = 0
    $skipped = 0
    while ($stack.Count -gt 0) {
        try { $children = @(Get-ChildItem -LiteralPath $stack.Pop() -Force) }
        catch { $skipped++; continue }
        foreach ($item in $children) {
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { $skipped++; continue }
            if ($item.PSIsContainer) {
                $directories.Add($item.FullName)
                $stack.Push($item.FullName)
                continue
            }
            try { Remove-Item -LiteralPath $item.FullName -Force; $deleted++ }
            catch { $skipped++ }
        }
    }
    $removedDirectories = 0
    for ($index = $directories.Count - 1; $index -ge 0; $index--) {
        try {
            Assert-NoReparse $directories[$index]
            [IO.Directory]::Delete($directories[$index], $false)
            $removedDirectories++
        } catch { $skipped++ }
    }
    Write-Output ('临时文件：删除 {0} 个文件和 {1} 个空目录，跳过占用、受保护、非空目录或重解析项 {2} 项。' -f $deleted, $removedDirectories, $skipped)
}

<#
@brief 捕获原生命令的错误和退出码，由调用方汇总，不中断后续通道清理。
#>
function Invoke-EventLogClear([string]$Tool, [string]$Name) {
    $ErrorActionPreference = 'Continue'
    $diagnostic = @(& $Tool cl $Name 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw ('{0}：{1}' -f $Name, (($diagnostic | ForEach-Object { [string]$_ }) -join ' '))
    }
}

function Clear-EventLogs([string]$Tool = (Join-Path $env:SystemRoot 'System32\wevtutil.exe')) {
    $channels = @(& $Tool el)
    if ($LASTEXITCODE -ne 0) { throw '枚举事件日志失败。' }
    $failures = New-Object 'System.Collections.Generic.List[string]'
    $success = 0
    foreach ($channel in $channels) {
        $name = $channel.Trim()
        if (-not $name) { continue }
        try { Invoke-EventLogClear $Tool $name; $success++ }
        catch { $failures.Add($_.Exception.Message) }
    }
    Write-Output ('事件日志：已清空 {0} 个通道，失败 {1} 个。' -f $success, $failures.Count)
    if ($failures.Count -gt 0) { throw ('以下日志通道清理失败：' + ($failures -join ', ')) }
}

function Clear-Records {
    $state = Assert-Target
    $user = $state.user
    if ($p.scheduled) {
        if (-not $p.user -or $p.user.sid -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$') { throw '定时任务缺少有效的目标用户。' }
        $user = $p.user
        $registered = Get-ItemProperty -LiteralPath ('HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\' + $user.sid)
        $actual = [Environment]::ExpandEnvironmentVariables($registered.ProfileImagePath)
        if ([IO.Path]::GetFullPath($actual).TrimEnd('\') -ne [IO.Path]::GetFullPath($user.profile).TrimEnd('\')) {
            throw '定时任务中的用户路径与系统登记不一致。'
        }
    }
    Assert-NoReparse $user.profile
    $failures = New-Object 'System.Collections.Generic.List[string]'
    if ($c.cleanup.rdp_history -or $c.cleanup.recent_files) {
        try { Clear-UserRegistry $user.sid $user.profile; Write-Output '已清理目标用户的注册表历史。' }
        catch { $failures.Add($_.Exception.Message) }
    }
    $recent = Join-Path $user.profile 'AppData\Roaming\Microsoft\Windows\Recent'
    if ($c.cleanup.rdp_history) {
        foreach ($path in @((Join-Path $user.profile 'Documents\Default.rdp'), (Join-Path $recent 'AutomaticDestinations\1bc392b8e104a00e.automaticDestinations-ms'), (Join-Path $recent 'CustomDestinations\1bc392b8e104a00e.customDestinations-ms'))) {
            try { Remove-OrdinaryFile $path } catch { $failures.Add($_.Exception.Message) }
        }
        Write-Output '已处理默认 RDP 文件和远程桌面跳转列表。'
    }
    if ($c.cleanup.recent_files) {
        foreach ($directory in @($recent, (Join-Path $recent 'AutomaticDestinations'), (Join-Path $recent 'CustomDestinations'))) {
            try {
                Assert-NoReparse $directory
                if (Test-Path -LiteralPath $directory) {
                    foreach ($item in Get-ChildItem -LiteralPath $directory -File -Force) {
                        if ($directory -eq $recent -and $item.Extension -ne '.lnk') { continue }
                        try { Remove-OrdinaryFile $item.FullName }
                        catch { $failures.Add($_.Exception.Message) }
                    }
                }
            } catch { $failures.Add($_.Exception.Message) }
        }
        foreach ($relative in @('AppData\Roaming\Microsoft\Windows\PowerShell\PSReadLine\ConsoleHost_history.txt', 'AppData\Roaming\Microsoft\PowerShell\PSReadLine\ConsoleHost_history.txt')) {
            try { Remove-OrdinaryFile (Join-Path $user.profile $relative) } catch { $failures.Add($_.Exception.Message) }
        }
        Write-Output '已处理最近访问、跳转列表、运行历史和 PowerShell 输入历史。'
    }
    if ($c.cleanup.temp_files) {
        foreach ($path in @((Join-Path $user.profile 'AppData\Local\Temp'), (Join-Path $env:SystemRoot 'Temp'))) {
            try { Clear-TempDirectory $path } catch { $failures.Add($_.Exception.Message) }
        }
    }
    if ($c.cleanup.event_logs) {
        try { Clear-EventLogs } catch { $failures.Add($_.Exception.Message) }
    }
    if ($failures.Count -gt 0) { throw ($failures -join [Environment]::NewLine) }
    Write-Output '清理阶段完成。'
}

function Prepare-ScheduleDirectory {
    $null = Assert-Target
    $directory = $p.directory
    Assert-NoReparse (Split-Path -Parent $directory)
    $existingTask = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    if ($existingTask -and $existingTask.State -eq 'Running') { throw '现有清理任务正在运行，请等任务结束后再更新。' }
    $credentialTask = Get-ScheduledTask -TaskName $credentialTaskName -ErrorAction SilentlyContinue
    if ($credentialTask -and $credentialTask.State -eq 'Running') { throw '远程凭据清理任务正在运行，请稍后更新。' }
    if (Test-Path -LiteralPath $directory) {
        Assert-NoReparse $directory
        if (-not (Test-Path -LiteralPath $directory -PathType Container)) { throw '任务安装路径被文件占用。' }
        $owner = (Get-Acl -LiteralPath $directory).GetOwner([Security.Principal.SecurityIdentifier]).Value
        $current = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
        if ($owner -notin @('S-1-5-18', 'S-1-5-32-544', $current)) { throw '任务目录属于其他用户，请由管理员检查后处理。' }
        foreach ($item in Get-ChildItem -LiteralPath $directory -Force) {
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.PSIsContainer) { throw '任务目录中存在意外的目录或重解析项。' }
        }
    } else { $null = New-Item -ItemType Directory -Path $directory }
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    $administrators = New-Object Security.Principal.SecurityIdentifier('S-1-5-32-544')
    $acl.SetOwner($administrators)
    foreach ($sid in @('S-1-5-18', 'S-1-5-32-544')) {
        $identity = New-Object Security.Principal.SecurityIdentifier($sid)
        $rule = New-Object Security.AccessControl.FileSystemAccessRule($identity, 'FullControl', 'ContainerInherit, ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $directory -AclObject $acl
    if ($existingTask) { $null = Disable-ScheduledTask -TaskName $taskName }
    if ($credentialTask) { $null = Disable-ScheduledTask -TaskName $credentialTaskName }
}

function Install-Schedule {
    $null = Assert-Target
    if (-not $p.user -or $p.user.sid -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$' -or -not $p.user.profile) {
        throw '安装计划任务需要明确的本地用户上下文。'
    }
    $arguments = 'clean --scheduled --config "' + $p.config_path + '" --user-sid "' + $p.user.sid + '" --user-profile "' + $p.user.profile + '"'
    $action = New-ScheduledTaskAction -Execute $p.executable -Argument $arguments -WorkingDirectory $p.directory
    $trigger = New-ScheduledTaskTrigger -Daily -At $c.schedule.daily_at
    $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
    $settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -MultipleInstances IgnoreNew -ExecutionTimeLimit (New-TimeSpan -Hours 1)
    $task = New-ScheduledTask -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description '清理指定用户的本机历史及已选择的系统日志。'
    $null = Register-ScheduledTask -TaskName $taskName -InputObject $task -Force
    if ($c.cleanup.rdp_credentials) {
        $credentialAction = New-ScheduledTaskAction -Execute $p.executable -Argument ('clean --credentials-only --config "' + $p.config_path + '"') -WorkingDirectory $p.directory
        $credentialPrincipal = New-ScheduledTaskPrincipal -UserId $p.user.sid -LogonType Interactive -RunLevel Highest
        $logonTrigger = New-ScheduledTaskTrigger -AtLogOn -User $p.user.sid
        $credentialTask = New-ScheduledTask -Action $credentialAction -Trigger @($trigger, $logonTrigger) -Principal $credentialPrincipal -Settings $settings -Description '在用户登录期间清理该用户的远程桌面凭据。'
        $null = Register-ScheduledTask -TaskName $credentialTaskName -InputObject $credentialTask -Force
        Write-Output '远程凭据通过独立用户任务清理：用户已登录时每天执行，并在用户登录时补充执行。'
    } else {
        $oldCredentialTask = Get-ScheduledTask -TaskName $credentialTaskName -ErrorAction SilentlyContinue
        if ($oldCredentialTask) { Unregister-ScheduledTask -TaskName $credentialTaskName -Confirm:$false }
    }
    Write-Output ('已安装每日 {0} 执行的清理任务，时间按服务器时区计算。' -f $c.schedule.daily_at)
}

try {
    switch ($p.action) {
        'status' { Get-State | ConvertTo-Json -Depth 5 -Compress }
        'validate-target' { $null = Assert-Target }
        'preflight' { Test-Configuration }
        'apply' { Apply-Configuration }
        'clean' { Clear-Records }
        'schedule-prepare' { Prepare-ScheduleDirectory }
        'schedule-install' { Install-Schedule }
        'schedule-remove' {
            $null = Assert-Target
            foreach ($name in @($taskName, $credentialTaskName)) {
                $task = Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue
                if ($task -and $task.State -eq 'Running') { throw '清理任务正在运行，请等任务结束后再删除。' }
            }
            foreach ($name in @($taskName, $credentialTaskName)) {
                $task = Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue
                if ($task) { Unregister-ScheduledTask -TaskName $name -Confirm:$false }
            }
            Write-Output '已删除清理计划任务；受限目录中的程序和配置副本予以保留。'
        }
        default { throw '未知操作。' }
    }
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
