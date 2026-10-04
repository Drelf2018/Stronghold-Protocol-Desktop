package main

// 注入的那段静音脚本，和调用它的那两行 Go，必须在名字上一致。单方面改名不会在任何地方报错：
// 表现只会是窗口关掉之后声音还响着，或者窗口回来了游戏却一直静着。这条检查以前也看着一段通知
// 脚本，而那段脚本这个程序已经没有了。

import (
	"strings"
	"testing"
)

func TestMuteScript(t *testing.T) {
	if !strings.Contains(muteJS, "window._mutePage") {
		t.Error("js/mute.js does not define window._mutePage, so nothing Go evaluates can work")
	}
	if !strings.Contains(muteJS, "AudioContext") {
		t.Error("js/mute.js never touches AudioContext: the game's sound would keep playing")
	}
	// 只挂起不恢复，就是"关过一次之后永远没声音"；只恢复不挂起则是白跑。
	for _, want := range []string{"suspend", "resume"} {
		if !strings.Contains(muteJS, want) {
			t.Errorf("js/mute.js never calls %s", want)
		}
	}
	for _, call := range []string{mutePageOn, mutePageOff} {
		if !strings.Contains(call, "_mutePage") {
			t.Errorf("脚本 Go 实际执行的是 %q，里面没有 _mutePage", call)
		}
	}
	if !strings.Contains(mutePageOn, "true") || !strings.Contains(mutePageOff, "false") {
		t.Errorf("两个调用没有区分开关：%q / %q", mutePageOn, mutePageOff)
	}
}
