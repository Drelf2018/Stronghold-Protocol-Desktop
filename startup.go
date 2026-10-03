//go:build windows

package main

// Autostart at logon: one command pointing at this program, written into the current user's
// startup entries.
//
// The registry rather than a shortcut dropped into the Startup folder - just as effective,
// but the tick state can be read straight back from the same place, so nobody has to go
// looking in a folder to find out whether it took effect.
//
// It uses golang.org/x/sys/windows/registry and starts no child process: this program is
// windowsgui, and any console child process needs extra work to keep its window from flashing up
// (see hiddenCommand in launcher.go).

import (
	"log/slog"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// runKey is the current user's startup entries. HKCU rather than HKLM: no administrator
// rights needed, and "start when this user logs in" is the user's own choice to begin with.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartCommand is the command written into the registry: the whole path is quoted,
// because the executable's name has spaces in it.
func autostartCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return `"` + exe + `"`, nil
}

// autostartEnabled reads whether autostart is set right now, and whether it would really
// start this program.
//
// An entry that holds a value but points somewhere else - the program was moved or renamed
// after it was written, which a folder that can be unpacked anywhere invites - is reported as
// not set. The tick is a promise about the next logon, and Windows would not keep that one.
// Reading it as set would also make the next click turn autostart off rather than repair it,
// because the menu asks for the state and then writes the opposite of what it was told.
//
// The value's name is appID, which must never change: a rename would leave a second, stale
// command behind in the startup entries.
func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	command, _, err := k.GetStringValue(appID)
	if err != nil || command == "" {
		return false
	}
	// 取不到自己的路径（几乎不会发生）时，照注册表说的算：一次查询失败不该被说成「没有启用」。
	want, err := autostartCommand()
	if err != nil {
		slog.Warn("autostart: cannot find this program's path", "error", err)
		return true
	}
	// 路径比较不区分大小写，Windows 的路径本来就是这样。
	return strings.EqualFold(command, want)
}

// setAutostart sets or clears autostart and reports the **actual result**: a registry write
// can be refused, and the menu's tick has to match the real state rather than the intention.
func setAutostart(on bool) bool {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		slog.Error("autostart: cannot open the startup key", "error", err)
		return autostartEnabled()
	}
	defer k.Close()

	if !on {
		if err := k.DeleteValue(appID); err != nil && err != registry.ErrNotExist {
			slog.Error("autostart: cannot remove the entry", "error", err)
		}
		return autostartEnabled()
	}
	command, err := autostartCommand()
	if err != nil {
		slog.Error("autostart: cannot find this program's path", "error", err)
		return autostartEnabled()
	}
	if err := k.SetStringValue(appID, command); err != nil {
		slog.Error("autostart: cannot write the entry", "error", err)
		return autostartEnabled()
	}
	slog.Info("autostart: entry written", "command", command)
	return autostartEnabled()
}
