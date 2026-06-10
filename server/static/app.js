// gitview client behavior. Everything degrades: with JS disabled the pages
// still browse, only the picker filter, finder, shortcuts, and line
// selection go away.
(function () {
  "use strict";

  var doc = document;
  var root = doc.documentElement;

  // ---- Theme toggle -----------------------------------------------------
  function currentMode() {
    var m = root.getAttribute("data-color-mode");
    if (m === "light" || m === "dark") return m;
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  doc.addEventListener("click", function (e) {
    var btn = e.target.closest(".js-theme-toggle");
    if (!btn) return;
    var next = currentMode() === "dark" ? "light" : "dark";
    root.setAttribute("data-color-mode", next);
    try { localStorage.setItem("gitview-color-mode", next); } catch (err) { /* private mode */ }
  });

  // ---- Copy buttons -------------------------------------------------------
  doc.addEventListener("click", function (e) {
    var btn = e.target.closest(".js-copy");
    if (!btn) return;
    var text = btn.getAttribute("data-copy-text");
    if (!text && btn.getAttribute("data-copy-target") === "blob") {
      var rows = doc.querySelectorAll(".js-code .code-cell");
      var parts = [];
      rows.forEach(function (c) { parts.push(c.textContent); });
      text = parts.join("\n");
    }
    if (!text) return;
    navigator.clipboard.writeText(text).then(function () {
      btn.classList.add("copied");
      setTimeout(function () { btn.classList.remove("copied"); }, 1500);
    });
  });

  // ---- Branch picker filter ----------------------------------------------
  doc.querySelectorAll(".js-branch-filter").forEach(function (input) {
    input.addEventListener("input", function () {
      var q = input.value.toLowerCase();
      var menu = input.closest(".SelectMenu-modal");
      menu.querySelectorAll(".js-pickable").forEach(function (item) {
        var name = (item.getAttribute("data-name") || "").toLowerCase();
        item.style.display = name.indexOf(q) === -1 ? "none" : "";
      });
    });
  });
  doc.querySelectorAll(".js-branch-picker").forEach(function (details) {
    details.addEventListener("toggle", function () {
      if (details.open) {
        var input = details.querySelector(".js-branch-filter");
        if (input) input.focus();
      }
    });
  });

  // ---- Line anchors: click selects, shift-click extends -------------------
  var lineRe = /^#L(\d+)(?:-L(\d+))?$/;
  function applyLineSelection() {
    var m = lineRe.exec(location.hash);
    doc.querySelectorAll(".code-row.selected").forEach(function (r) {
      r.classList.remove("selected");
    });
    if (!m) return;
    var lo = parseInt(m[1], 10);
    var hi = m[2] ? parseInt(m[2], 10) : lo;
    if (hi < lo) { var t = lo; lo = hi; hi = t; }
    for (var i = lo; i <= hi; i++) {
      var row = doc.getElementById("L" + i);
      if (row) row.classList.add("selected");
    }
    var first = doc.getElementById("L" + lo);
    if (first && !applyLineSelection.scrolled) {
      applyLineSelection.scrolled = true;
      first.scrollIntoView({ block: "center" });
    }
  }
  doc.addEventListener("click", function (e) {
    var num = e.target.closest(".code-num");
    if (!num || !num.closest(".js-code")) return;
    var line = parseInt(num.getAttribute("data-line-number"), 10);
    if (!line) return;
    var m = lineRe.exec(location.hash);
    if (e.shiftKey && m) {
      var anchor = parseInt(m[1], 10);
      var lo = Math.min(anchor, line);
      var hi = Math.max(anchor, line);
      location.hash = lo === hi ? "L" + lo : "L" + lo + "-L" + hi;
    } else {
      location.hash = "L" + line;
    }
  });
  window.addEventListener("hashchange", applyLineSelection);
  applyLineSelection();

  // ---- File finder ----------------------------------------------------------
  var finder = doc.querySelector(".finder");
  if (finder) {
    var input = finder.querySelector(".js-finder-input");
    var list = finder.querySelector(".js-finder-list");
    var blobBase = finder.getAttribute("data-blob-base");
    var paths = null;
    var active = 0;

    fetch(finder.getAttribute("data-tree-list"), { headers: { Accept: "application/json" } })
      .then(function (r) {
        if (!r.ok) throw new Error("tree list " + r.status);
        return r.json();
      })
      .then(function (data) {
        paths = data.paths || [];
        renderResults();
      })
      .catch(function () {
        list.innerHTML = '<li class="finder-empty text-muted">Could not load the file list.</li>';
      });

    // Subsequence match, scored toward GitHub's feel: consecutive runs,
    // path-boundary hits, and basename hits rank higher.
    function fuzzy(q, p) {
      var qi = 0, score = 0, run = 0, hits = [];
      var lower = p.toLowerCase();
      for (var i = 0; i < lower.length && qi < q.length; i++) {
        if (lower[i] === q[qi]) {
          hits.push(i);
          score += 1 + run * 2;
          if (i === 0 || p[i - 1] === "/" || p[i - 1] === "_" || p[i - 1] === "-" || p[i - 1] === ".") score += 4;
          run++;
          qi++;
        } else {
          run = 0;
        }
      }
      if (qi < q.length) return null;
      var slash = p.lastIndexOf("/");
      if (hits.length && hits[0] > slash) score += 6;
      score -= p.length / 100;
      return { score: score, hits: hits };
    }

    function highlight(p, hits) {
      var out = "";
      var set = {};
      hits.forEach(function (h) { set[h] = true; });
      for (var i = 0; i < p.length; i++) {
        var c = p[i].replace(/&/g, "&amp;").replace(/</g, "&lt;");
        out += set[i] ? "<mark>" + c + "</mark>" : c;
      }
      return out;
    }

    function renderResults() {
      if (!paths) return;
      var q = input.value.trim().toLowerCase();
      var rows;
      if (!q) {
        rows = paths.slice(0, 50).map(function (p) { return { path: p, hits: [] }; });
      } else {
        rows = [];
        for (var i = 0; i < paths.length; i++) {
          var m = fuzzy(q, paths[i]);
          if (m) rows.push({ path: paths[i], score: m.score, hits: m.hits });
        }
        rows.sort(function (a, b) { return b.score - a.score; });
        rows = rows.slice(0, 50);
      }
      if (!rows.length) {
        list.innerHTML = '<li class="finder-empty text-muted">No matching files.</li>';
        return;
      }
      list.innerHTML = rows
        .map(function (r, i) {
          return '<li' + (i === 0 ? ' class="active"' : "") + '><a href="' + blobBase + r.path + '">' + highlight(r.path, r.hits) + "</a></li>";
        })
        .join("");
      active = 0;
    }

    function move(delta) {
      var items = list.querySelectorAll("li");
      if (!items.length) return;
      items[active] && items[active].classList.remove("active");
      active = (active + delta + items.length) % items.length;
      items[active].classList.add("active");
      items[active].scrollIntoView({ block: "nearest" });
    }

    input.addEventListener("input", renderResults);
    input.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown") { e.preventDefault(); move(1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); move(-1); }
      else if (e.key === "Enter") {
        var a = list.querySelector("li.active a");
        if (a) location.href = a.href;
      } else if (e.key === "Escape") {
        history.back();
      }
    });
  }

  // ---- Keyboard shortcuts -----------------------------------------------------
  var help = doc.getElementById("shortcut-help");
  function toggleHelp(show) {
    if (!help) return;
    if (show === undefined) show = help.hidden;
    help.hidden = !show;
    help.classList.toggle("shortcut-open", show);
  }
  doc.addEventListener("keydown", function (e) {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    var tag = (e.target.tagName || "").toLowerCase();
    if (tag === "input" || tag === "textarea" || tag === "select" || e.target.isContentEditable) {
      if (e.key === "Escape") e.target.blur();
      return;
    }
    if (e.key === "Escape") { toggleHelp(false); return; }
    if (e.key === "?") { toggleHelp(); return; }
    if (e.key === "t") {
      var go = doc.querySelector('[data-hotkey="t"]');
      if (go) { e.preventDefault(); location.href = go.getAttribute("href"); }
      return;
    }
    if (e.key === "b") {
      var blame = doc.querySelector('[data-hotkey="b"]');
      if (blame) { e.preventDefault(); location.href = blame.getAttribute("href"); }
      return;
    }
    if (e.key === "y") {
      // Pin the URL to the commit SHA, github's permalink shortcut. The
      // tree link in the latest-commit bar carries the resolved SHA.
      var sha = doc.querySelector(".file-box-header .commit-sha, .commit-sha-full .commit-sha");
      var m = location.pathname.match(/^(\/[^/]+\/[^/]+\/(?:blob|tree|blame|raw)\/)([^/]+)(\/.*)?$/);
      if (m && sha) {
        var full = sha.getAttribute("href");
        var id = full ? full.split("/").pop() : sha.textContent.trim();
        location.pathname = m[1] + id + (m[3] || "");
      }
    }
  });
})();
