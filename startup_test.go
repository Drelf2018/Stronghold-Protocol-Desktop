package main

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// runValue reads the command that is written in there right now; nothing to read means
// empty.
func runValue(t *testing.T) string {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	command, _, err := k.GetStringValue(appID)
	if err != nil {
		return ""
	}
	return command
}

// putRunValue writes exactly this command into the startup entry, and removes the entry when
// the command is empty.
//
// The string goes in as it stands rather than through setAutostart, because a test that has to
// put back what it found must not write what the program would write today. setAutostart
// writes the path of the running executable, and under go test that is a binary in a temporary
// build directory: leaving one behind would point the user's autostart at a folder that is
// about to be deleted, and nothing would say a word at the next logon.
func putRunValue(t *testing.T, command string) {
	t.Helper()
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("打开启动项：%v", err)
	}
	defer k.Close()
	if command == "" {
		if err := k.DeleteValue(appID); err != nil && err != registry.ErrNotExist {
			t.Fatalf("清掉启动项：%v", err)
		}
		return
	}
	if err := k.SetStringValue(appID, command); err != nil {
		t.Fatalf("写启动项：%v", err)
	}
}

// restoreRun puts the startup entry back exactly as it was found - a command, or no entry at
// all - when the test ends.
func restoreRun(t *testing.T) {
	t.Helper()
	before := runValue(t)
	t.Cleanup(func() {
		putRunValue(t, before)
		if got := runValue(t); got != before {
			t.Errorf("清理失败：注册表里是 %q，原本是 %q", got, before)
		}
	})
}

// Autostart is written into the registry, so this test really writes, really reads back, and
// restores what it found. What it checks is the thing the menu has to show: the tick state
// comes from what the registry really holds, not from an intention kept in memory.
func TestAutostart(t *testing.T) {
	restoreRun(t)

	if !setAutostart(true) || !autostartEnabled() {
		t.Fatal("设上自启动之后，读回来仍然是没有")
	}
	command, err := autostartCommand()
	if err != nil {
		t.Fatalf("取可执行文件路径：%v", err)
	}
	// 程序名里有空格，命令必须整条带引号：不加引号的话 Windows 会把
	// 「C:...Stronghold Protocol Launcher.exe」拆成三个参数，登录时什么都不会启动。
	if !strings.HasPrefix(command, `"`) || !strings.HasSuffix(command, `"`) {
		t.Fatalf("命令行没有加引号：%q", command)
	}
	if got := runValue(t); got != command {
		t.Fatalf("注册表里是 %q，应当是 %q", got, command)
	}

	if setAutostart(false) || autostartEnabled() {
		t.Fatal("撤掉自启动之后，读回来仍然是有")
	}
	if got := runValue(t); got != "" {
		t.Fatalf("撤掉之后注册表里还剩 %q", got)
	}
}

// An entry written before the program was moved or renamed still holds a value, but the
// command in it starts nothing. It has to read as not set - the tick is a promise about the
// next logon - and the click after it has to repair the entry rather than remove it, which is
// only true if the stale entry counts as "off" to begin with.
func TestAutostartStaleEntry(t *testing.T) {
	restoreRun(t)

	putRunValue(t, `"C:moved-awayStronghold Protocol Launcher.exe"`)
	if autostartEnabled() {
		t.Error("注册表里那条命令指向别处，却被当成了「已启用」")
	}

	if !setAutostart(true) || !autostartEnabled() {
		t.Error("勾一下之后，读回来仍然是没有")
	}
	want, err := autostartCommand()
	if err != nil {
		t.Fatalf("取可执行文件路径：%v", err)
	}
	if got := runValue(t); got != want {
		t.Errorf("勾一下之后注册表里是 %q，应当是 %q", got, want)
	}
}
