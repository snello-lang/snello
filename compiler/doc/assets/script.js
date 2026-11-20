// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The script of snello doc's pages: it chooses the theme before the page
// is drawn, the reader's last choice or the system's, flips it from the
// button, and filters the sidebar, and the index's names, as the search
// box is typed in. The server serves it beside the pages; a page loads
// it in its head, so that the theme is chosen before the page is drawn.
(function () {
    var key = "snello-doc-theme";
    var saved = localStorage.getItem(key);
    if (saved) {
        document.documentElement.dataset.theme = saved;
    }

    document.addEventListener("DOMContentLoaded", function () {
        document.getElementById("theme").addEventListener("click", function () {
            var root = document.documentElement;
            var current = root.dataset.theme || (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
            var next = current === "dark" ? "light" : "dark";
            root.dataset.theme = next;
            localStorage.setItem(key, next);
        });

        var filter = document.getElementById("filter");
        filter.addEventListener("input", function () {
            var query = filter.value.toLowerCase();
            document.querySelectorAll("[data-name]").forEach(function (entry) {
                entry.hidden = query !== "" && entry.dataset.name.toLowerCase().indexOf(query) < 0;
            });

            var names = document.getElementById("all-names");
            if (names) {
                names.hidden = query === "";
                document.getElementById("packages").hidden = query !== "";
            }
        });
    });
})();
