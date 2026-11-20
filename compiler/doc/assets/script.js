// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The script of snello doc's pages: it chooses the theme before the page
// is drawn, the reader's last choice or the system's, and flips it from
// the button; filters every package and name from the box at the foot
// of the tree, on /, ⌘K or Ctrl-K, by their letters in order, from
// /search.json, loaded the first time; colors the keywords, strings,
// comments and numbers of the code; puts a copy button on every block
// of code; copies the page as text; opens the tree at the current
// package; and marks in the outline what is being read. The server
// serves it beside the pages; a page loads it in its head, so that the
// theme is chosen before the page is drawn.
(function () {
    // Where the theme is kept, the system's choice of a dark one, how
    // many of what the filter finds are shown, where it looks, what a
    // copy button says and for how long, and how far below the top of
    // the window a section is being read.
    var themeKey = "snello-doc-theme";
    var darkQuery = "(prefers-color-scheme: dark)";
    var shownResults = 30;
    var searchURL = "/search.json";
    var copiedLabel = "Copied", failedLabel = "Copy failed", labelPause = 1200;
    var readingTop = 140;

    // keywords are the language's, which a test holds to the compiler's
    // own list.
    var keywords = ["break", "case", "chan", "const", "continue", "default", "defer", "else", "for", "func", "spawn",
        "if", "import", "map", "package", "range", "return", "select", "struct", "switch", "type", "var",
        "trait", "impl", "enum", "match", "ref", "mut", "Self", "super"];

    // lexeme finds, left to right, a comment, a string or rune, a
    // keyword or a number, each in its own group, so that a keyword
    // inside a string or a comment stays the string's or the comment's.
    var lexeme = new RegExp(/(\/\/.*|\/\*[\s\S]*?\*\/)|("(?:[^"\\\n]|\\.)*"|`[^`]*`|'(?:[^'\\\n]|\\.)*')|\b(KEYWORDS)\b|\b(\d[\w.]*)/
        .source.replace("KEYWORDS", keywords.join("|")), "g");
    var lexemeClasses = ["tok-comment", "tok-string", "tok-keyword", "tok-number"];

    var saved = localStorage.getItem(themeKey);
    if (saved) {
        document.documentElement.dataset.theme = saved;
    }

    // lettersInOrder reports how far apart query's letters are found in
    // text, in order, nothing for two found together, or -1 when they are
    // not all found.
    function lettersInOrder(query, text) {
        var at = 0, total = 0, last = -2;
        for (var i = 0; i < query.length; i++) {
            var found = text.indexOf(query[i], at);
            if (found < 0) {
                return -1;
            }

            total += found === last + 1 ? 0 : found - at + 1;
            last = found;
            at = found + 1;
        }

        return total;
    }

    // score reports how well query matches text, lower being better, or
    // -1 when it does not: its letters in order in the last part of text,
    // after its last dot or slash, before anywhere in the whole of it,
    // and best at that part's start; a shorter text first among equals.
    function score(query, text) {
        var whole = text.toLowerCase();
        var name = whole.slice(Math.max(whole.lastIndexOf("."), whole.lastIndexOf("/")) + 1);
        var inName = lettersInOrder(query, name);
        if (inName >= 0) {
            return (name.indexOf(query) === 0 ? 0 : 10) + inName + whole.length / 100;
        }

        var inWhole = lettersInOrder(query, whole);
        return inWhole < 0 ? -1 : 100 + inWhole + whole.length / 100;
    }

    // highlight writes a block of code again with each lexeme in a span
    // of its class.
    function highlight(pre) {
        var text = pre.textContent, out = document.createDocumentFragment(), at = 0, m;
        lexeme.lastIndex = 0;
        while ((m = lexeme.exec(text))) {
            out.append(text.slice(at, m.index));
            var span = document.createElement("span");
            span.className = lexemeClasses[m.slice(1).findIndex(Boolean)];
            span.textContent = m[0];
            out.append(span);
            at = lexeme.lastIndex;
        }

        out.append(text.slice(at));
        pre.replaceChildren(out);
    }

    // copied tells on button whether the copy written settled or failed.
    function copied(written, button) {
        var was = button.textContent;
        function say(label) {
            button.textContent = label;
            setTimeout(function () { button.textContent = was; }, labelPause);
        }

        written.then(function () { say(copiedLabel); }, function () { say(failedLabel); });
    }

    document.addEventListener("DOMContentLoaded", function () {
        document.getElementById("doc-theme").addEventListener("click", function () {
            var root = document.documentElement;
            var current = root.dataset.theme || (matchMedia(darkQuery).matches ? "dark" : "light");
            var next = current === "dark" ? "light" : "dark";
            root.dataset.theme = next;
            localStorage.setItem(themeKey, next);
        });

        // The filter: what it finds takes the tree's place until it is
        // emptied.
        var tree = document.getElementById("doc-tree");
        var filter = document.getElementById("doc-filter");
        var list = document.getElementById("doc-filter-results");
        var items = null, shown = [], selected = 0;

        function render() {
            list.replaceChildren();
            shown.forEach(function (item, i) {
                var li = document.createElement("li");
                if (i === selected) {
                    li.className = "selected";
                }

                var a = document.createElement("a");
                a.href = item.h;
                var kind = document.createElement("span");
                kind.className = "kind kind-" + item.k;
                kind.title = item.k;
                a.append(kind, item.t);
                if (item.s) {
                    var s = document.createElement("small");
                    s.textContent = item.s;
                    a.append(s);
                }

                li.append(a);
                list.append(li);
            });

            var current = list.children[selected];
            if (current) {
                current.scrollIntoView({ block: "nearest" });
            }
        }

        function search() {
            var query = filter.value.toLowerCase().replace(/\s+/g, "");
            tree.hidden = query !== "";
            list.hidden = query === "";
            if (query === "" || !items) {
                return;
            }

            shown = items
                .map(function (item) { return { item: item, score: score(query, item.t) }; })
                .filter(function (r) { return r.score >= 0; })
                .sort(function (a, b) { return a.score - b.score; })
                .slice(0, shownResults)
                .map(function (r) { return r.item; });
            selected = 0;
            render();
        }

        // load fetches what the filter looks through, once; a fetch that
        // fails is tried again the next time the filter is used.
        function load() {
            if (items) {
                return;
            }

            items = [];
            fetch(searchURL).then(function (r) { return r.json(); }).then(function (all) {
                items = all || [];
                search();
            }).catch(function () {
                items = null;
            });
        }

        filter.addEventListener("focus", load);
        filter.addEventListener("input", search);
        filter.addEventListener("keydown", function (e) {
            if (e.key === "ArrowDown") {
                selected = Math.min(selected + 1, shown.length - 1);
                render();
                e.preventDefault();
            } else if (e.key === "ArrowUp") {
                selected = Math.max(selected - 1, 0);
                render();
                e.preventDefault();
            } else if (e.key === "Enter" && !list.hidden && shown[selected]) {
                location.href = shown[selected].h;
            } else if (e.key === "Escape") {
                filter.value = "";
                search();
                filter.blur();
            }
        });
        document.addEventListener("keydown", function (e) {
            var typing = /^(INPUT|TEXTAREA)$/.test(document.activeElement.tagName);
            if ((e.key === "k" && (e.metaKey || e.ctrlKey)) || (e.key === "/" && !typing)) {
                e.preventDefault();
                filter.focus();
                filter.select();
            }
        });

        // The tree opens at the current package.
        var current = tree.querySelector(".current");
        if (current) {
            tree.scrollTop = current.offsetTop - tree.clientHeight / 3;
        }

        // The code's colors, but an example's output, which is text.
        document.querySelectorAll(".code-block pre:not(.output + pre)").forEach(highlight);

        // A copy button on every block of code, and the page's.
        document.querySelectorAll(".code-block").forEach(function (block) {
            var button = document.createElement("button");
            button.type = "button";
            button.className = "copy-code";
            button.textContent = "Copy";
            button.addEventListener("click", function () {
                copied(navigator.clipboard.writeText(block.querySelector("pre").textContent), button);
            });
            block.appendChild(button);
        });

        // The page's text is written to the clipboard as the click's own
        // write, with the text still to come, since a browser may refuse
        // a write made after the click's moment has passed.
        var copyPage = document.querySelector(".copy-page");
        if (copyPage) {
            copyPage.addEventListener("click", function () {
                var text = fetch(copyPage.dataset.text).then(function (r) {
                    if (!r.ok) {
                        throw new Error(r.statusText);
                    }

                    return r.text();
                }).then(function (t) { return new Blob([t], { type: "text/plain" }); });
                copied(navigator.clipboard.write([new ClipboardItem({ "text/plain": text })]), copyPage);
            });
        }

        // The outline marks the last section whose top has scrolled past,
        // once a frame however often the page scrolls.
        var links = Array.prototype.slice.call(document.querySelectorAll(".toc a"));
        var targets = links.map(function (a) { return document.getElementById(decodeURIComponent(a.hash.slice(1))); });
        var marked = -1, pending = false;
        function mark() {
            pending = false;
            var current = 0;
            targets.forEach(function (t, i) {
                if (t && t.getBoundingClientRect().top < readingTop) {
                    current = i;
                }
            });

            if (current !== marked) {
                links.forEach(function (a, i) { a.classList.toggle("active", i === current); });
                marked = current;
            }
        }

        if (links.length) {
            document.addEventListener("scroll", function () {
                if (!pending) {
                    pending = true;
                    requestAnimationFrame(mark);
                }
            }, { passive: true });
            mark();
        }
    });
})();
