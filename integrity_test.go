package main

// 完整性级别是**量出来的**，不是假设的，所以值得有一条测试证明它真的被量了。量出来是什么
// 取决于跑测试的人：普通账户是 medium，沙箱里是 low——所以这里只钉住那套词汇，具体值打印出
// 来给读输出的人看。

import "testing"

func TestProcessIntegrity(t *testing.T) {
	level := processIntegrity()
	t.Logf("this process runs at integrity %q", level)
	switch level {
	case "untrusted", "low", "medium", "high", "system":
	default:
		t.Errorf("processIntegrity() = %q, which is not one of the levels this program names", level)
	}
}
