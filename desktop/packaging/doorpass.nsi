; DoorPass installer for Windows, built by desktop/build.sh:
;   makensis -DVERSION=x.y.z -DSRC=<staged app dir> -DOUTFILE=<setup.exe> -DICON=<icon.ico> doorpass.nsi
; Installs per user (no admin). The app installs its helper service itself,
; behind one UAC prompt.

Unicode true
SetCompressor /SOLID lzma

!include "MUI2.nsh"

Name "DoorPass"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\DoorPass"
RequestExecutionLevel user
VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "DoorPass"
VIAddVersionKey "FileDescription" "DoorPass installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "Based on Intra by Jigsaw Operations LLC. Apache License 2.0."

!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"
!define MUI_FINISHPAGE_RUN "$INSTDIR\DoorPass.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open DoorPass"

; What DoorPass changes and sends, shown before anything is installed.
!define MUI_PAGE_HEADER_TEXT "Before you install"
!define MUI_PAGE_HEADER_SUBTEXT "What DoorPass changes on this computer, and what it sends."
!define MUI_LICENSEPAGE_TEXT_TOP "Please read this before installing DoorPass."
!define MUI_LICENSEPAGE_BUTTON "&Next >"
!define MUI_LICENSEPAGE_TEXT_BOTTOM "Click Next to continue."
!insertmacro MUI_PAGE_LICENSE "install-notice.txt"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\DoorPass"

Section "DoorPass"
  nsExec::Exec 'taskkill /IM DoorPass.exe /F'
  SetOutPath "$INSTDIR"
  File /r "${SRC}\*.*"
  WriteUninstaller "$INSTDIR\Uninstall DoorPass.exe"
  CreateShortcut "$SMPROGRAMS\DoorPass.lnk" "$INSTDIR\DoorPass.exe"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayName" "DoorPass"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTKEY}" "Publisher" "DoorPass"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\DoorPass.exe"
  WriteRegStr HKCU "${UNINSTKEY}" "UninstallString" '"$INSTDIR\Uninstall DoorPass.exe"'
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoRepair" 1
SectionEnd

Section "Uninstall"
  nsExec::Exec 'taskkill /IM DoorPass.exe /F'
  ; Remove the helper service too (one UAC prompt; skipping it leaves the service).
  ExecShellWait "runas" "$INSTDIR\helper\doorpass-helper.exe" "uninstall" SW_HIDE
  Delete "$SMPROGRAMS\DoorPass.lnk"
  RMDir /r "$INSTDIR"
  DeleteRegKey HKCU "${UNINSTKEY}"
SectionEnd
