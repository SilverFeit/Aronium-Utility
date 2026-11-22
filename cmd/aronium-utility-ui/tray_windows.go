//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32dll   = windows.NewLazySystemDLL("user32.dll")
	shell32dll  = windows.NewLazySystemDLL("shell32.dll")
	kernel32dll = windows.NewLazySystemDLL("kernel32.dll")

	procGetModuleHandleW       = kernel32dll.NewProc("GetModuleHandleW")
	procRegisterClassExW       = user32dll.NewProc("RegisterClassExW")
	procCreateWindowExW        = user32dll.NewProc("CreateWindowExW")
	procDestroyWindow          = user32dll.NewProc("DestroyWindow")
	procDefWindowProcW         = user32dll.NewProc("DefWindowProcW")
	procGetMessageW            = user32dll.NewProc("GetMessageW")
	procTranslateMessage       = user32dll.NewProc("TranslateMessage")
	procDispatchMessageW       = user32dll.NewProc("DispatchMessageW")
	procPostMessageW           = user32dll.NewProc("PostMessageW")
	procPostQuitMessage        = user32dll.NewProc("PostQuitMessage")
	procLoadImageW             = user32dll.NewProc("LoadImageW")
	procLoadIconW              = user32dll.NewProc("LoadIconW")
	procDestroyIcon            = user32dll.NewProc("DestroyIcon")
	procGetSystemMetrics       = user32dll.NewProc("GetSystemMetrics")
	procCreatePopupMenu        = user32dll.NewProc("CreatePopupMenu")
	procDestroyMenu            = user32dll.NewProc("DestroyMenu")
	procAppendMenuW            = user32dll.NewProc("AppendMenuW")
	procSetMenuDefaultItem     = user32dll.NewProc("SetMenuDefaultItem")
	procGetCursorPos           = user32dll.NewProc("GetCursorPos")
	procSetForegroundWindow    = user32dll.NewProc("SetForegroundWindow")
	procTrackPopupMenu         = user32dll.NewProc("TrackPopupMenu")
	procMessageBoxW            = user32dll.NewProc("MessageBoxW")
	procRegisterWindowMessageW = user32dll.NewProc("RegisterWindowMessageW")
	procShellNotifyIconW       = shell32dll.NewProc("Shell_NotifyIconW")
)

const (
	wmNull          = 0x0000
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmContextMenu   = 0x007B
	wmCommand       = 0x0111
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmApp           = 0x8000
	wmTrayIcon      = wmApp + 1
	wmTrayQuit      = wmApp + 2
	wmTrayBalloon   = wmApp + 3

	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	niifInfo    = 0x00000001
	niifWarning = 0x00000002
	niifError   = 0x00000003

	mfString    = 0x00000000
	mfChecked   = 0x00000008
	mfUnchecked = 0x00000000
	mfGrayed    = 0x00000001
	mfSeparator = 0x00000800

	tpmRightButton = 0x0002
	tpmBottomAlign = 0x0020
	tpmReturnCmd   = 0x0100

	imageIcon       = 1
	lrDefaultSize   = 0x00000040
	lrShared        = 0x00008000
	lrLoadFromFile  = 0x00000010
	smCxSmIcon      = 49
	smCySmIcon      = 50
	idiApplication  = 32512
	mbOK            = 0x00000000
	mbYesNo         = 0x00000004
	mbIconError     = 0x00000010
	mbIconQuestion  = 0x00000020
	mbIconInfo      = 0x00000040
	mbSetForeground = 0x00010000
	mbTopMost       = 0x00040000
	idYes           = 6

	cmdTrayOpenUI          = 1001
	cmdTrayToggleAutostart = 1002
	cmdTrayUpdate          = 1003
	cmdTrayExitAll         = 1004
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type winPoint struct {
	X int32
	Y int32
}

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      winPoint
}

type notifyIconDataW struct {
	cbSize           uint32
	hwnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uTimeout         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     uintptr
}

type trayCallbacks struct {
	OnOpenUI          func()
	IsAutostartOn     func() bool
	OnToggleAutostart func() (enabled bool, statusMsg string, err error)
	OnCheckAndUpdate  func(showBalloon func(title, msg string, flags uint32), confirm func(title, msg string) bool)
	GetAvailableTag   func() string
	OnExitAll         func()
}

type trayController struct {
	mu           sync.Mutex
	hwnd         uintptr
	hIcon        uintptr
	ownIcon      bool
	taskbarMsgID uint32
	cb           trayCallbacks
	lastLeftOpen time.Time
	removed      bool
}

var (
	globalTrayMu sync.Mutex
	globalTray   *trayController
)

func runDesktopTrayLoop(cb trayCallbacks) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ctrl := &trayController{cb: cb}
	globalTrayMu.Lock()
	globalTray = ctrl
	globalTrayMu.Unlock()

	hInst, _, _ := procGetModuleHandleW.Call(0)
	hIcon, owned := loadTrayHIcon(hInst)
	ctrl.hIcon = hIcon
	ctrl.ownIcon = owned

	taskbarCreatedName, _ := windows.UTF16PtrFromString("TaskbarCreated")
	tbMsg, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(taskbarCreatedName)))
	ctrl.taskbarMsgID = uint32(tbMsg)

	className, _ := windows.UTF16PtrFromString("AroniumUtilityTrayClass")
	winTitle, _ := windows.UTF16PtrFromString("Aronium Utility Tray")

	wndProc := syscall.NewCallback(trayWndProc)
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   wndProc,
		hInstance:     hInst,
		hIcon:         hIcon,
		hIconSm:       hIcon,
		lpszClassName: className,
	}
	_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, createErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(winTitle)),
		0,
		0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		ctrl.cleanupIcon()
		return fmt.Errorf("CreateWindowExW failed: %v", createErr)
	}
	ctrl.hwnd = hwnd

	if err := ctrl.addNotifyIcon(); err != nil {
		_, _, _ = procDestroyWindow.Call(hwnd)
		ctrl.cleanupIcon()
		return err
	}

	var msg winMsg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	ctrl.deleteNotifyIcon()
	ctrl.cleanupIcon()
	return nil
}

func stopDesktopTrayLoop() {
	globalTrayMu.Lock()
	ctrl := globalTray
	globalTrayMu.Unlock()
	if ctrl == nil {
		return
	}
	ctrl.mu.Lock()
	hwnd := ctrl.hwnd
	ctrl.mu.Unlock()
	if hwnd != 0 {
		_, _, _ = procPostMessageW.Call(hwnd, wmTrayQuit, 0, 0)
	}
}

func loadTrayHIcon(hInst uintptr) (uintptr, bool) {
	cx, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	cy, _, _ := procGetSystemMetrics.Call(smCySmIcon)
	if cx == 0 {
		cx = 16
	}
	if cy == 0 {
		cy = 16
	}

	// 1. Embedded RT_GROUP_ICON resource ID 1 from aronium-utility-ui.exe
	if hIcon, _, _ := procLoadImageW.Call(hInst, 1, imageIcon, cx, cy, lrShared); hIcon != 0 {
		return hIcon, false
	}
	if hIcon, _, _ := procLoadIconW.Call(hInst, 1); hIcon != 0 {
		return hIcon, false
	}

	// 2. Fallback to sibling app-icon.ico
	if exe, err := os.Executable(); err == nil {
		icoPath := filepath.Join(filepath.Dir(exe), "app-icon.ico")
		if _, err := os.Stat(icoPath); err == nil {
			if p, err := windows.UTF16PtrFromString(icoPath); err == nil {
				if hIcon, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon, cx, cy, lrLoadFromFile|lrDefaultSize); hIcon != 0 {
					return hIcon, true
				}
			}
		}
	}

	// 3. Fallback standard Windows application icon
	hFallback, _, _ := procLoadIconW.Call(0, idiApplication)
	return hFallback, false
}

func copyUTF16Fixed(dst []uint16, s string) {
	units := windows.StringToUTF16(s)
	n := len(units)
	if n > len(dst) {
		n = len(dst)
	}
	for i := 0; i < n; i++ {
		dst[i] = units[i]
	}
	if len(dst) > 0 {
		dst[len(dst)-1] = 0
	}
}

func (c *trayController) addNotifyIcon() error {
	nid := notifyIconDataW{
		cbSize:           uint32(unsafe.Sizeof(notifyIconDataW{})),
		hwnd:             c.hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTrayIcon,
		hIcon:            c.hIcon,
	}
	copyUTF16Fixed(nid.szTip[:], fmt.Sprintf("Aronium Utility v%s — Activo en segundo plano", AppVersion))
	ret, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if ret == 0 {
		return fmt.Errorf("Shell_NotifyIconW(NIM_ADD) failed: %v", err)
	}
	c.mu.Lock()
	c.removed = false
	c.mu.Unlock()
	return nil
}

func (c *trayController) deleteNotifyIcon() {
	c.mu.Lock()
	if c.removed || c.hwnd == 0 {
		c.mu.Unlock()
		return
	}
	c.removed = true
	hwnd := c.hwnd
	c.mu.Unlock()

	nid := notifyIconDataW{
		cbSize: uint32(unsafe.Sizeof(notifyIconDataW{})),
		hwnd:   hwnd,
		uID:    1,
	}
	_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func (c *trayController) showBalloon(title, msg string, flags uint32) {
	c.mu.Lock()
	hwnd := c.hwnd
	hIcon := c.hIcon
	removed := c.removed
	c.mu.Unlock()
	if hwnd == 0 || removed {
		return
	}
	if flags == 0 {
		flags = niifInfo
	}
	nid := notifyIconDataW{
		cbSize:      uint32(unsafe.Sizeof(notifyIconDataW{})),
		hwnd:        hwnd,
		uID:         1,
		uFlags:      nifInfo | nifIcon | nifTip,
		hIcon:       hIcon,
		uTimeout:    4500,
		dwInfoFlags: flags,
	}
	copyUTF16Fixed(nid.szTip[:], fmt.Sprintf("Aronium Utility v%s — Activo en segundo plano", AppVersion))
	copyUTF16Fixed(nid.szInfoTitle[:], title)
	copyUTF16Fixed(nid.szInfo[:], msg)
	_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (c *trayController) cleanupIcon() {
	if c.ownIcon && c.hIcon != 0 {
		_, _, _ = procDestroyIcon.Call(c.hIcon)
		c.hIcon = 0
		c.ownIcon = false
	}
}

func showWindowsMessageBox(title, body string, flags uint32) bool {
	tPtr, _ := windows.UTF16PtrFromString(title)
	bPtr, _ := windows.UTF16PtrFromString(body)
	ret, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(bPtr)), uintptr(unsafe.Pointer(tPtr)), uintptr(flags|mbSetForeground|mbTopMost))
	return ret == idYes
}

func notifyDialog(title, body string, isError bool) {
	flags := uint32(mbOK | mbIconInfo)
	if isError {
		flags = mbOK | mbIconError
	}
	_ = showWindowsMessageBox(title, body, flags)
}

func (c *trayController) showContextMenu() {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer func() {
		_, _, _ = procDestroyMenu.Call(hMenu)
	}()

	addItem := func(flags uint32, id uintptr, text string) {
		var txtPtr uintptr
		if text != "" {
			p, _ := windows.UTF16PtrFromString(text)
			txtPtr = uintptr(unsafe.Pointer(p))
		}
		_, _, _ = procAppendMenuW.Call(hMenu, uintptr(flags), id, txtPtr)
	}

	addItem(mfString, cmdTrayOpenUI, fmt.Sprintf("Abrir Aronium Utility (v%s)", AppVersion))
	_, _, _ = procSetMenuDefaultItem.Call(hMenu, 0, 1)
	addItem(mfSeparator, 0, "")

	autoFlags := uint32(mfString | mfUnchecked)
	if c.cb.IsAutostartOn != nil && c.cb.IsAutostartOn() {
		autoFlags = mfString | mfChecked
	}
	addItem(autoFlags, cmdTrayToggleAutostart, "Iniciar al encender el sistema (Abrir automáticamente)")

	updateLabel := "Buscar y actualizar desde GitHub"
	if c.cb.GetAvailableTag != nil {
		if tag := c.cb.GetAvailableTag(); tag != "" {
			updateLabel = fmt.Sprintf("Actualizar a %s (¡Disponible!)", tag)
		}
	}
	addItem(mfString, cmdTrayUpdate, updateLabel)
	addItem(mfSeparator, 0, "")
	addItem(mfString, cmdTrayExitAll, "Cerrar del todo")

	var pt winPoint
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	_, _, _ = procSetForegroundWindow.Call(c.hwnd)

	cmdID, _, _ := procTrackPopupMenu.Call(
		hMenu,
		tpmRightButton|tpmBottomAlign|tpmReturnCmd,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		c.hwnd,
		0,
	)
	_, _, _ = procPostMessageW.Call(c.hwnd, wmNull, 0, 0)

	c.handleMenuCommand(uint32(cmdID))
}

func (c *trayController) handleMenuCommand(cmdID uint32) {
	switch cmdID {
	case cmdTrayOpenUI:
		if c.cb.OnOpenUI != nil {
			go c.cb.OnOpenUI()
		}

	case cmdTrayToggleAutostart:
		if c.cb.OnToggleAutostart != nil {
			go func() {
				enabled, statusMsg, err := c.cb.OnToggleAutostart()
				if err != nil {
					c.showBalloon("Inicio automático", fmt.Sprintf("No se pudo cambiar: %v", err), niifError)
					return
				}
				if enabled {
					c.showBalloon("Inicio automático activado", "Aronium Utility se abrirá automáticamente al encender el PC.", niifInfo)
				} else {
					c.showBalloon("Inicio automático desactivado", statusMsg, niifInfo)
				}
			}()
		}

	case cmdTrayUpdate:
		if c.cb.OnCheckAndUpdate != nil {
			go c.cb.OnCheckAndUpdate(
				func(title, msg string, flags uint32) {
					c.showBalloon(title, msg, flags)
				},
				func(title, msg string) bool {
					return showWindowsMessageBox(title, msg, mbYesNo|mbIconQuestion)
				},
			)
		}

	case cmdTrayExitAll:
		c.deleteNotifyIcon()
		if c.cb.OnExitAll != nil {
			c.cb.OnExitAll()
		}
		_, _, _ = procDestroyWindow.Call(c.hwnd)
		_, _, _ = procPostQuitMessage.Call(0)
	}
}

func trayWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	globalTrayMu.Lock()
	ctrl := globalTray
	globalTrayMu.Unlock()

	if ctrl != nil && ctrl.taskbarMsgID != 0 && msg == ctrl.taskbarMsgID {
		_ = ctrl.addNotifyIcon()
		return 0
	}

	switch msg {
	case wmTrayIcon:
		if ctrl == nil {
			break
		}
		switch uint32(lParam) {
		case wmRButtonUp, wmContextMenu:
			ctrl.showContextMenu()
		case wmLButtonUp, wmLButtonDblClk:
			ctrl.mu.Lock()
			since := time.Since(ctrl.lastLeftOpen)
			if since > 650*time.Millisecond {
				ctrl.lastLeftOpen = time.Now()
				ctrl.mu.Unlock()
				if ctrl.cb.OnOpenUI != nil {
					go ctrl.cb.OnOpenUI()
				}
			} else {
				ctrl.mu.Unlock()
			}
		}
		return 0

	case wmCommand:
		if ctrl != nil {
			ctrl.handleMenuCommand(uint32(wParam & 0xFFFF))
		}
		return 0

	case wmTrayQuit:
		if ctrl != nil {
			ctrl.deleteNotifyIcon()
		}
		_, _, _ = procDestroyWindow.Call(hwnd)
		_, _, _ = procPostQuitMessage.Call(0)
		return 0

	case wmClose:
		if ctrl != nil {
			ctrl.deleteNotifyIcon()
		}
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0

	case wmDestroy:
		if ctrl != nil {
			ctrl.deleteNotifyIcon()
		}
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}
