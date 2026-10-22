package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	advapi32 = windows.NewLazySystemDLL("advapi32.dll")

	procGetModuleHandleW      = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW      = user32.NewProc("RegisterClassExW")
	procCreateWindowExW       = user32.NewProc("CreateWindowExW")
	procDefWindowProcW        = user32.NewProc("DefWindowProcW")
	procDestroyWindow         = user32.NewProc("DestroyWindow")
	procPostQuitMessage       = user32.NewProc("PostQuitMessage")
	procGetMessageW           = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessageW      = user32.NewProc("DispatchMessageW")
	procSendMessageW          = user32.NewProc("SendMessageW")
	procLoadIconW             = user32.NewProc("LoadIconW")
	procCreatePopupMenu       = user32.NewProc("CreatePopupMenu")
	procAppendMenuW           = user32.NewProc("AppendMenuW")
	procDestroyMenu           = user32.NewProc("DestroyMenu")
	procTrackPopupMenuEx      = user32.NewProc("TrackPopupMenuEx")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procGetCursorPos          = user32.NewProc("GetCursorPos")
	procShell_NotifyIconW     = shell32.NewProc("Shell_NotifyIconW")
	procRegOpenKeyExW         = advapi32.NewProc("RegOpenKeyExW")
	procRegSetValueExW        = advapi32.NewProc("RegSetValueExW")
	procRegDeleteValueW       = advapi32.NewProc("RegDeleteValueW")
	procRegQueryValueExW      = advapi32.NewProc("RegQueryValueExW")
	procRegCloseKey           = advapi32.NewProc("RegCloseKey")
)

const (
	WM_APP         = 0x8000
	WM_TRAYICON    = WM_APP + 1
	WM_DESTROY     = 0x0002
	WM_LBUTTONUP   = 0x0202
	WM_RBUTTONUP   = 0x0205
	WM_COMMAND     = 0x0111

	NIM_ADD        = 0x00000000
	NIM_MODIFY     = 0x00000001
	NIM_DELETE     = 0x00000002

	NIF_MESSAGE    = 0x00000001
	NIF_ICON       = 0x00000002
	NIF_TIP        = 0x00000004
	NIF_INFO       = 0x00000010

	MF_STRING      = 0x00000000
	MF_GRAYED      = 0x00000001
	MF_SEPARATOR   = 0x00000800
	MF_CHECKED     = 0x00000008
	MF_UNCHECKED   = 0x00000000

	TPM_RIGHTBUTTON = 0x0002
	TPM_BOTTOMALIGN = 0x0020

	IDI_APPLICATION = 32512

	HKEY_CURRENT_USER = 0x80000001
	KEY_READ          = 0x20019
	KEY_WRITE         = 0x20006
	REG_SZ            = 1

	CMD_REFRESH     = 1001
	CMD_AUTOSTART   = 1002
	CMD_GITHUB      = 1003
	CMD_EXIT        = 1004
)

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             windows.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UTimeoutOrVersion uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}

type POINT struct {
	X int32
	Y int32
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

type TrayApp struct {
	hwnd  windows.Handle
	store *Store
	nid   NOTIFYICONDATAW
}

var currentTray *TrayApp

func NewTrayApp(store *Store) *TrayApp {
	t := &TrayApp{
		store: store,
	}
	currentTray = t
	return t
}

func (t *TrayApp) Run() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	className, _ := windows.UTF16PtrFromString("LimitBarTrayClass")
	windowTitle, _ := windows.UTF16PtrFromString("LimitBar")

	hInst, _, _ := procGetModuleHandleW.Call(0)
	hIcon, _, _ := procLoadIconW.Call(0, uintptr(IDI_APPLICATION))

	wcex := WNDCLASSEXW{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
		LpfnWndProc:   windows.NewCallback(wndProc),
		HInstance:     windows.Handle(hInst),
		HIcon:         windows.Handle(hIcon),
		LpszClassName: className,
	}

	ret, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcex)))
	if ret == 0 {
		return fmt.Errorf("RegisterClassExW failed: %w", err)
	}

	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowTitle)),
		0,
		0, 0, 0, 0,
		0, 0,
		uintptr(hInst),
		0,
	)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW failed: %w", err)
	}
	t.hwnd = windows.Handle(hwnd)

	t.nid = NOTIFYICONDATAW{
		CbSize:           uint32(unsafe.Sizeof(NOTIFYICONDATAW{})),
		HWnd:             t.hwnd,
		UID:              1,
		UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
		UCallbackMessage: WM_TRAYICON,
		HIcon:            windows.Handle(hIcon),
	}
	copy(t.nid.SzTip[:], windows.StringToUTF16("LimitBar - AI Coding Limits"))

	procShell_NotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&t.nid)))
	defer procShell_NotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&t.nid)))

	t.UpdateTooltip()

	var msg struct {
		HWnd    windows.Handle
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      POINT
	}

	for {
		res, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(res) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	return nil
}

func (t *TrayApp) UpdateTooltip() {
	installed := t.store.Installed()
	var parts []string
	for _, v := range installed {
		if snap, ok := t.store.Get(v); ok && snap.State == StateOK && snap.Bar != nil && snap.Bar.Used != nil {
			parts = append(parts, fmt.Sprintf("%s: %s%%", v.Name(), PercentText(snap.Bar.Used)))
		}
	}
	tip := "LimitBar"
	if len(parts) > 0 {
		tip = "LimitBar: " + strings.Join(parts, " | ")
	}
	if len(tip) > 120 {
		tip = tip[:117] + "..."
	}
	utf16Tip := windows.StringToUTF16(tip)
	for i := range t.nid.SzTip {
		if i < len(utf16Tip) {
			t.nid.SzTip[i] = utf16Tip[i]
		} else {
			t.nid.SzTip[i] = 0
		}
	}
	t.nid.UFlags = NIF_TIP
	procShell_NotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&t.nid)))
}

func wndProc(hwnd windows.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_TRAYICON:
		switch lParam {
		case WM_RBUTTONUP, WM_LBUTTONUP:
			showContextMenu(hwnd)
		}
		return 0

	case WM_COMMAND:
		switch wParam {
		case CMD_REFRESH:
			if currentTray != nil {
				go func() {
					currentTray.store.RefreshAll()
					currentTray.UpdateTooltip()
				}()
			}
		case CMD_AUTOSTART:
			toggleAutostart()
		case CMD_GITHUB:
			_ = exec.Command("cmd", "/c", "start", "https://github.com/w13/limitbar").Start()
		case CMD_EXIT:
			procPostQuitMessage.Call(0)
		}
		return 0

	case WM_DESTROY:
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func showContextMenu(hwnd windows.Handle) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	appendMenuItem := func(id uintptr, text string, flags uintptr) {
		pText, _ := windows.UTF16PtrFromString(text)
		procAppendMenuW.Call(hMenu, flags, id, uintptr(unsafe.Pointer(pText)))
	}

	appendMenuItem(0, "LimitBar v1.1.0 for Windows", MF_GRAYED|MF_STRING)
	appendMenuItem(0, "", MF_SEPARATOR)

	if currentTray != nil {
		installed := currentTray.store.Installed()
		for _, v := range installed {
			snap, ok := currentTray.store.Get(v)
			if !ok || snap.State != StateOK {
				continue
			}
			label := fmt.Sprintf("%s: Active", v.Name())
			if snap.Bar != nil && snap.Bar.Used != nil {
				label = fmt.Sprintf("%s: %s%% used", v.Name(), PercentText(snap.Bar.Used))
			}
			if len(snap.Notes) > 0 {
				label += " (" + snap.Notes[0] + ")"
			}
			appendMenuItem(0, label, MF_GRAYED|MF_STRING)
		}
		if len(installed) > 0 {
			appendMenuItem(0, "", MF_SEPARATOR)
		}
	}

	appendMenuItem(CMD_REFRESH, "Refresh All Quotas", MF_STRING)

	autostartFlag := uintptr(MF_UNCHECKED)
	if isAutostartEnabled() {
		autostartFlag = uintptr(MF_CHECKED)
	}
	appendMenuItem(CMD_AUTOSTART, "Start at Windows Login", MF_STRING|autostartFlag)
	appendMenuItem(CMD_GITHUB, "GitHub Repository...", MF_STRING)
	appendMenuItem(0, "", MF_SEPARATOR)
	appendMenuItem(CMD_EXIT, "Exit LimitBar", MF_STRING)

	var pt POINT
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	procSetForegroundWindow.Call(uintptr(hwnd))
	procTrackPopupMenuEx.Call(hMenu, TPM_RIGHTBUTTON|TPM_BOTTOMALIGN, uintptr(pt.X), uintptr(pt.Y), uintptr(hwnd), 0)
	procSendMessageW.Call(uintptr(hwnd), 0, 0, 0)
}

func isAutostartEnabled() bool {
	var hKey windows.Handle
	subKey, _ := windows.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Run`)
	res, _, _ := procRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(subKey)), 0, KEY_READ, uintptr(unsafe.Pointer(&hKey)))
	if res != 0 {
		return false
	}
	defer procRegCloseKey.Call(uintptr(hKey))

	valName, _ := windows.UTF16PtrFromString("LimitBar")
	res, _, _ = procRegQueryValueExW.Call(uintptr(hKey), uintptr(unsafe.Pointer(valName)), 0, 0, 0, 0)
	return res == 0
}

func toggleAutostart() {
	var hKey windows.Handle
	subKey, _ := windows.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Run`)
	valName, _ := windows.UTF16PtrFromString("LimitBar")

	if isAutostartEnabled() {
		res, _, _ := procRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(subKey)), 0, KEY_WRITE, uintptr(unsafe.Pointer(&hKey)))
		if res == 0 {
			procRegDeleteValueW.Call(uintptr(hKey), uintptr(unsafe.Pointer(valName)))
			procRegCloseKey.Call(uintptr(hKey))
		}
	} else {
		res, _, _ := procRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(subKey)), 0, KEY_WRITE, uintptr(unsafe.Pointer(&hKey)))
		if res == 0 {
			exePath, err := exec.LookPath(os.Args[0])
			if err != nil {
				exePath = os.Args[0]
			}
			pVal, _ := windows.UTF16PtrFromString(fmt.Sprintf("\"%s\"", exePath))
			valLen := uintptr((len(windows.StringToUTF16(fmt.Sprintf("\"%s\"", exePath)))) * 2)
			procRegSetValueExW.Call(uintptr(hKey), uintptr(unsafe.Pointer(valName)), 0, REG_SZ, uintptr(unsafe.Pointer(pVal)), valLen)
			procRegCloseKey.Call(uintptr(hKey))
		}
	}
}
