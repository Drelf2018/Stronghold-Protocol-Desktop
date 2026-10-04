//go:build windows

package main

// 本进程跑在什么完整性级别，以及那意味着什么。
//
// 值得量一量，因为它独立于 ACL 决定进程能写什么：
// 低完整性进程在用户目录里根本哪儿都写不了——%LOCALAPPDATA% 不行，
// %TEMP% 也不行，因为那些是 medium——WebView2 也建不出自己的用户数据目录。
// 沙箱、受限令牌，或者一个把受限令牌发出去的启动器，才会把进程
// 放到那个级别。
//
// 这种组合才叫人困惑：从一个 ACL 明明授予了 FullControl
// 的目录里得到「Access is denied」。这就是那套解释缺的另一半，而它只值一行日志。

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// processIntegrity 命名当前进程所处的级别：「untrusted」「low」「medium」
// 「high」「system」，取不到令牌时是「unknown」。
func processIntegrity() string {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return "unknown"
	}
	defer token.Close()

	buf := make([]byte, 256)
	var size uint32
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buf[0], uint32(len(buf)), &size); err != nil {
		return "unknown"
	}
	// TOKEN_MANDATORY_LABEL { SID_AND_ATTRIBUTES Label }：头部存着一个指向 SID 的指针，
	// 而 SID 本身就在同一个缓冲区里、紧跟在它后面。
	label := (*windows.SIDAndAttributes)(unsafe.Pointer(&buf[0]))
	sid := label.Sid
	if sid == nil || sid.SubAuthorityCount() == 0 {
		return "unknown"
	}
	switch rid := sid.SubAuthority(uint32(sid.SubAuthorityCount() - 1)); {
	case rid >= 0x4000:
		return "system"
	case rid >= 0x3000:
		return "high"
	case rid >= 0x2000:
		return "medium"
	case rid >= 0x1000:
		return "low"
	default:
		return "untrusted"
	}
}

// integrityNote 是消息框追加的那句话：级别本身，以及——当它是写不了用户自己目录
// 的级别时——那意味着什么、该怎么办。下面这两个级别都不是双击能得到的，
// 所以在这里点明原因，就是「让人觉得莫名其妙的失败」与「能照着解决的失败」之间的
// 全部差别。
func integrityNote(level string) string {
	if level == "low" || level == "untrusted" {
		return "\n\n本进程的完整性级别是 " + level + "：这个级别的进程写不进用户目录（%LOCALAPPDATA%、%TEMP% 都是 medium），" +
			"WebView2 也就建不出自己的数据目录。它通常意味着本程序是被沙箱或受限环境拉起来的——" +
			"请在资源管理器里直接双击它，而不是从别的应用里的链接打开。"
	}
	return "\n\n本进程的完整性级别：" + level
}
