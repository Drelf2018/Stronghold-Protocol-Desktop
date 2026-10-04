package main

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// runValue 读出此刻写在那里的那条命令；没有可读的，就是空。
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

// putRunValue 把这一条命令原样写进启动项；命令为空时把那一项删掉。
//
// 这里直接塞字符串，而不是走 setAutostart：一条「要把原样放回去」的测试，不能写程序今天会写的
// 那个值。setAutostart 写的是正在跑的那个可执行文件的路径，而在 go test 下那是一个临时构建目录
// 里的二进制——留下它就会把用户的自启动指向一个马上要被删掉的目录，而下一次登录时没有任何东西
// 会说一句话。
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

// restoreRun 在测试结束时，把启动项原样放回去——一条命令，或者根本没有那一项。
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

// 自启动是写进注册表的，所以这条测试是真的写、真的读回来，再把原样放回去。它查的正是菜单必须
// 显示的那件事：那个勾来自注册表里**真实**有的东西，不是内存里存着的一个意图。
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

// 一份在程序被搬走或改名之前写下的启动项，值还在，但里面那条命令什么都启动不了。它必须读作
// 「没有启用」——那个勾是对下一次登录的承诺——而紧接着的那一次点击要修好这一项，而不是把它删掉；
// 而这一点只有在旧项一开始就算作「关」的时候才成立。
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
