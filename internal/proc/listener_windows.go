package proc

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getExtendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// Read the listener tables directly. Get-NetTCPConnection -State Listen
// can omit a listener whose state the CIM provider does not expose.
func listeningOn(ctx context.Context, port int) ([]int, error) {
	var ps []int
	seen := map[int]bool{}
	// MIB_TCPROW_OWNER_PID and MIB_TCP6ROW_OWNER_PID use DWORD
	// fields, except for the IPv6 addresses. The port is in network order.
	for _, table := range []struct{ family, width, localPort int }{
		{windows.AF_INET, 24, 8}, {windows.AF_INET6, 56, 20},
	} {
		data, err := tcpListeners(ctx, table.family)
		if err != nil {
			return nil, err
		}
		if len(data) < 4 {
			return nil, errors.New("invalid Windows TCP listener table")
		}
		count := binary.LittleEndian.Uint32(data)
		if uint64(count)*uint64(table.width) > uint64(len(data)-4) {
			return nil, errors.New("truncated Windows TCP listener table")
		}
		for row := range int(count) {
			start := 4 + row*table.width
			if int(binary.BigEndian.Uint16(data[start+table.localPort:])) != port {
				continue
			}
			pid := int(binary.LittleEndian.Uint32(data[start+table.width-4:]))
			if pid > 0 && !seen[pid] {
				seen[pid] = true
				ps = append(ps, pid)
			}
		}
	}
	return ps, nil
}

func tcpListeners(ctx context.Context, family int) ([]byte, error) {
	const ownerPIDListener = 3
	var data []byte
	var size uint32
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, _, _ := getExtendedTCPTable.Call(
			uintptr(unsafe.Pointer(unsafe.SliceData(data))), uintptr(unsafe.Pointer(&size)),
			0, uintptr(family), ownerPIDListener, 0,
		)
		switch err := syscall.Errno(result); err {
		case 0:
			if uint64(size) > uint64(len(data)) {
				return nil, errors.New("invalid Windows TCP listener table size")
			}
			return data[:size], ctx.Err()
		case windows.ERROR_INSUFFICIENT_BUFFER:
			data = make([]byte, size)
		default:
			return nil, err
		}
	}
}

func executable(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}

// terminate ends it at once: a tray app has no console for a Ctrl+Break,
// and no window a close message would reach.
func terminate(pid int) error { return kill(pid) }

func kill(pid int) error { return exitWith(pid, 1) }

// end ends pid with exit code 0, which its parent reads as a clean exit:
// Windows has no SIGTERM for a console program without a window.
func end(pid int) error { return exitWith(pid, 0) }

func exitWith(pid int, code uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return ErrDenied
		}
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil // gone already
		}
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, code); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return ErrDenied
		}
		return err
	}
	return nil
}

// processName is Get-Process' name, which names a service's process where
// opening it for its path is denied.
func processName(pid int) string {
	out, err := CommandContext(context.Background(), "powershell", "-NoProfile", "-NonInteractive", "-Command",
		`(Get-Process -Id `+strconv.Itoa(pid)+` -ErrorAction SilentlyContinue).ProcessName`).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
