// The game's fullscreen button (public/js/ui/device.js) goes through the standard Fullscreen API, and
// inside WebView2 that only makes the page fill the control: the window keeps its frame and its size.
// Making the *window* fullscreen is Go's job - see fullscreen.go - so all this does is report what the
// page did, through the _fullscreen binding.
//
// Only the events are hooked, never the calls. fullscreenchange fires for every way in and out - the
// button, Esc, the page deciding on its own - so the page's state stays the source of truth, and Go
// never reaches back into the page, which is what keeps the two from bouncing.
(function () {
    if (window._fullscreenHook) {
        return;
    }
    window._fullscreenHook = true;

    function tell() {
        if (!window._fullscreen) {
            return;
        }
        window._fullscreen(!!(document.fullscreenElement || document.webkitFullscreenElement));
    }

    document.addEventListener('fullscreenchange', tell);
    document.addEventListener('webkitfullscreenchange', tell);
})();
