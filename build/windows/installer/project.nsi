Unicode true

####
## KNcloud-WIN 安装包（Setup.exe）。
##
## 由 CI（.github/workflows/build.yml）直接用 makensis 编译，不再走 `wails build -nsis`：
## 安装内容就是 tools/pack 组装好的绿色版文件夹（KNcloud.exe + bin\ 下的 geo 文件、wintun.dll、
## badvpn-tun2socks.exe、许可文件……），与发布的 KNcloud-WIN-<tag>.zip 完全一致，
## 应用内更新下载 zip 后可以原地替换安装目录里的文件。
##
## 本地编译（在本目录）：
##   go run ../../../tools/pack -exe <wails 产物 exe> -out ..\..\..\dist -version v1.2.3
##   makensis -DSRC_DIR=..\..\..\dist\KNcloud -DVERSION=v1.2.3 -DVI_VERSION=1.2.3.0 ^
##            -DOUT_FILE=..\..\..\dist\KNcloud-WIN-v1.2.3-Setup.exe project.nsi
##
## 数据：安装目录里写入标记文件 KNcloud.installed，程序据此把配置 / 日志放在 %APPDATA%\KNcloud
## （见 paths.go / internal/instlayout）。卸载时保留 %APPDATA%\KNcloud，只删程序文件。
####

!ifndef SRC_DIR
  !error "SRC_DIR (tools/pack 输出的 KNcloud 文件夹) 未指定"
!endif
!ifndef VERSION
  !define VERSION "dev"
!endif
!ifndef VI_VERSION
  !define VI_VERSION "0.0.0.0"
!endif
!ifndef OUT_FILE
  !define OUT_FILE "KNcloud-WIN-${VERSION}-Setup.exe"
!endif

!define PRODUCT_NAME     "KNcloud-WIN"
!define PRODUCT_EXE      "KNcloud.exe"              ; 与 tools/pack、应用内更新包里的 exe 名一致
!define PRODUCT_PUBLISHER "KNcloud"
!define INSTALL_MARKER   "KNcloud.installed"        ; 与 internal/instlayout.MarkerName 一致
!define TASK_NAME        "KNcloud-WIN"              ; 与 internal/autostart.TaskName 一致
!define UNINST_KEY       "Software\Microsoft\Windows\CurrentVersion\Uninstall\KNcloud-WIN"

!include "MUI2.nsh"
!include "x64.nsh"
!include "WinVer.nsh"
!include "FileFunc.nsh"
!include "LogicLib.nsh"

Name "${PRODUCT_NAME}"
OutFile "${OUT_FILE}"
InstallDir "$PROGRAMFILES64\${PRODUCT_NAME}"
InstallDirRegKey HKLM "${UNINST_KEY}" "InstallLocation"
RequestExecutionLevel admin
ShowInstDetails show
ShowUninstDetails show
SetCompressor /SOLID lzma
ManifestDPIAware true

VIProductVersion "${VI_VERSION}"
VIFileVersion    "${VI_VERSION}"
VIAddVersionKey "CompanyName"     "${PRODUCT_PUBLISHER}"
VIAddVersionKey "FileDescription" "${PRODUCT_NAME} Installer"
VIAddVersionKey "ProductVersion"  "${VERSION}"
VIAddVersionKey "FileVersion"     "${VERSION}"
VIAddVersionKey "LegalCopyright"  "${PRODUCT_PUBLISHER}"
VIAddVersionKey "ProductName"     "${PRODUCT_NAME}"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXE}"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

; 程序正在运行时文件被占用：提示用户先从托盘退出（不强杀，避免系统代理没被还原）。
!macro EnsureNotRunning
  ${Do}
    ; cmd /C 的整条命令要再包一层引号：cmd 会去掉第一个和最后一个引号，不包的话
    ; tasklist 路径的引号被拆坏、命令执行失败（返回非 0），等于永远检测不到正在运行。
    nsExec::ExecToStack '"$SYSDIR\cmd.exe" /C ""$SYSDIR\tasklist.exe" /FI "IMAGENAME eq ${PRODUCT_EXE}" /NH | "$SYSDIR\find.exe" /I "${PRODUCT_EXE}""'
    Pop $0 ; find 返回 0 = 找到了正在运行的进程
    Pop $1
    ${If} $0 != 0
      ${Break}
    ${EndIf}
    ${If} ${Silent}
      SetErrorLevel 2
      Quit
    ${EndIf}
    MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "KNcloud 正在运行。请先在托盘图标上右键「退出」，然后点「重试」。$\r$\nKNcloud is running. Please exit it from the tray icon, then click Retry." /SD IDCANCEL IDRETRY +2
    Quit
  ${Loop}
!macroend

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_OK|MB_ICONSTOP "KNcloud-WIN 只支持 64 位 Windows 10 / 11。"
    Abort
  ${EndIf}
  ${IfNot} ${AtLeastWin10}
    MessageBox MB_OK|MB_ICONSTOP "KNcloud-WIN 需要 Windows 10 或更高版本。"
    Abort
  ${EndIf}
  SetRegView 64
FunctionEnd

Function un.onInit
  SetRegView 64
FunctionEnd

Section "Install"
  SetShellVarContext all
  !insertmacro EnsureNotRunning

  SetOutPath "$INSTDIR"
  ; 与便携 zip 完全一致的内容（KNcloud.exe + bin\...）
  File /r "${SRC_DIR}\*.*"
  ; 安装版标记：程序据此把配置 / 日志放到 %APPDATA%\KNcloud
  FileOpen $0 "$INSTDIR\${INSTALL_MARKER}" w
  FileWrite $0 "Installed by ${PRODUCT_NAME} Setup ${VERSION}. Settings live in %APPDATA%\KNcloud.$\r$\n"
  FileClose $0

  CreateShortcut "$SMPROGRAMS\${PRODUCT_NAME}.lnk" "$INSTDIR\${PRODUCT_EXE}" "" "$INSTDIR\${PRODUCT_EXE}" 0
  CreateShortcut "$DESKTOP\${PRODUCT_NAME}.lnk" "$INSTDIR\${PRODUCT_EXE}" "" "$INSTDIR\${PRODUCT_EXE}" 0

  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${PRODUCT_NAME}"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "${PRODUCT_PUBLISHER}"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_EXE}"
  WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
  WriteRegStr HKLM "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKLM "${UNINST_KEY}" "EstimatedSize" "$0"
SectionEnd

Section "uninstall"
  SetShellVarContext all
  !insertmacro EnsureNotRunning

  ; 开机自启：删除程序创建的计划任务 \KNcloud-WIN（新版本）与旧版本写的 HKCU Run 自启项
  nsExec::Exec '"$SYSDIR\schtasks.exe" /Delete /TN "${TASK_NAME}" /F'
  Pop $0
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "KNcloud-WIN"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "KNcloud"
  ; kncloud:// 协议（程序运行时注册在 HKCU）
  DeleteRegKey HKCU "Software\Classes\kncloud"

  Delete "$SMPROGRAMS\${PRODUCT_NAME}.lnk"
  Delete "$DESKTOP\${PRODUCT_NAME}.lnk"

  ; 只删程序文件。用户数据在 %APPDATA%\KNcloud，保留不动；
  ; 安装目录里万一有 configs\ / logs\（例如手动放进来的绿色版数据）也保留，RMDir 只删空目录。
  Delete "$INSTDIR\${PRODUCT_EXE}"
  Delete "$INSTDIR\${PRODUCT_EXE}.old"
  Delete "$INSTDIR\${INSTALL_MARKER}"
  RMDir /r "$INSTDIR\bin"
  RMDir /r "$INSTDIR\update"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"

  ; WebView2 缓存（Wails 默认 %APPDATA%\<exe 名>，即 %APPDATA%\KNcloud.exe；不是 %APPDATA%\KNcloud）
  SetShellVarContext current
  RMDir /r "$APPDATA\${PRODUCT_EXE}"

  DeleteRegKey HKLM "${UNINST_KEY}"
SectionEnd
