// The join prompt. Go has no way to ask a question on screen - there is no console, and a message box
// cannot take a line of text back - so the question is asked in the page and the answer goes back
// through the _join binding (see setURL in main.go).
//
// What gets typed is one of three things: a whole invite link from a friend, a host or host:port, or
// the four-character room key on its own. Which one it is, and how to complete it, is decided on the
// Go side (joinURL), so this file only has to ask.
function askForRoom(current) {
    var input = prompt('粘贴朋友发来的加入链接，或者直接输入 4 位同盟密钥：', current);
    if (input === null) {
        return;
    }
    input = input.trim();
    if (input && input !== current && window._join) {
        window._join(input);
    }
}
