// The page's sound belongs to the page: public/js/audio.js builds an AudioContext on the first
// gesture and suspends it when the document goes hidden.
//
// That is exactly why minimising the window silences the game - Chromium marks the page hidden and
// the page quiets itself - and why hiding it (which is what the close button does here) did not: a
// hidden window is still a visible *page*, so that visibilitychange never fires.
//
// Go cannot ask WebView2 to settle it instead: the controller that owns IsVisible lives behind
// jchv/go-webview2's public API and is not reachable from here.
//
// So this script, registered for every document before any page script runs, does two things:
// keeps hold of every AudioContext the page creates, and exposes
//
//   window._mutePage(true)     the window is going away
//   window._mutePage(false)    the window is back
(function () {
    if (window._mutePage) return;

    var muted = false;
    var contexts = [];

    var Native = window.AudioContext || window.webkitAudioContext;
    if (Native) {
        // A function that returns an object: under "new" the returned object is the result, so the
        // page gets a real AudioContext. Reflect.construct with Native as the target keeps the
        // prototype right, which is what "instanceof AudioContext" reads.
        var Tracked = function () {
            var ctx = Reflect.construct(Native, arguments, Native);
            contexts.push(ctx);
            if (muted) {
                try { ctx.suspend(); } catch (e) { /* not started yet; it will be caught later */ }
            }
            return ctx;
        };
        Tracked.prototype = Native.prototype;
        window.AudioContext = Tracked;
        window.webkitAudioContext = Tracked;
    }

    window._mutePage = function (on) {
        muted = !!on;
        for (var i = 0; i < contexts.length; i++) {
            var ctx = contexts[i];
            try {
                if (muted) { if (ctx.state === 'running') ctx.suspend(); }
                else if (ctx.state === 'suspended') ctx.resume();
            } catch (e) { /* one context misbehaving must not strand the rest */ }
        }
        // No <audio> element in this game today, but a page that swaps how it plays sound should
        // not go silent here just because this script only knew about one of the two ways.
        var media = document.querySelectorAll('audio, video');
        for (var j = 0; j < media.length; j++) { media[j].muted = muted; }
    };
})();
