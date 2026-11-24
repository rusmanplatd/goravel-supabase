(function () {
  var root = document.documentElement;
  var themeBtn = document.getElementById("theme-toggle");
  if (themeBtn) {
    themeBtn.addEventListener("click", function () {
      var next = root.getAttribute("data-theme") === "light" ? "dark" : "light";
      root.setAttribute("data-theme", next);
      try {
        localStorage.setItem("gsb-theme", next);
      } catch (e) {}
    });
  }

  var menuBtn = document.getElementById("menu-toggle");
  var sidebar = document.getElementById("sidebar");
  var backdrop = document.getElementById("sidebar-backdrop");
  function toggleSidebar(force) {
    if (!sidebar || !backdrop) return;
    var open = typeof force === "boolean" ? force : !sidebar.classList.contains("open");
    sidebar.classList.toggle("open", open);
    backdrop.classList.toggle("open", open);
  }
  if (menuBtn) {
    menuBtn.addEventListener("click", function () {
      toggleSidebar();
    });
  }
  if (backdrop) {
    backdrop.addEventListener("click", function () {
      toggleSidebar(false);
    });
  }

  var searchInput = document.getElementById("nav-search");
  if (searchInput) {
    searchInput.addEventListener("input", function () {
      var q = searchInput.value.trim().toLowerCase();
      document.querySelectorAll("#sidebar-nav .nav-link").forEach(function (link) {
        var match = !q || link.textContent.toLowerCase().indexOf(q) !== -1;
        link.classList.toggle("hidden", !match);
      });
    });
  }

  document.querySelectorAll(".codehilite").forEach(function (block) {
    var pre = block.querySelector("pre");
    if (!pre) return;
    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = "copy-btn";
    btn.textContent = "Copy";
    btn.addEventListener("click", function () {
      if (!navigator.clipboard) return;
      navigator.clipboard.writeText(pre.innerText).then(function () {
        btn.textContent = "Copied";
        setTimeout(function () {
          btn.textContent = "Copy";
        }, 1500);
      });
    });
    block.appendChild(btn);
  });

  var tocLinks = Array.prototype.slice.call(document.querySelectorAll(".toc a"));
  if (tocLinks.length) {
    var headings = tocLinks
      .map(function (a) {
        var id = decodeURIComponent(a.getAttribute("href").slice(1));
        return { link: a, el: document.getElementById(id) };
      })
      .filter(function (item) {
        return item.el !== null;
      });

    function onScroll() {
      var scrollY = window.scrollY + 100;
      var active = headings[0];
      for (var i = 0; i < headings.length; i++) {
        if (headings[i].el.offsetTop <= scrollY) {
          active = headings[i];
        }
      }
      tocLinks.forEach(function (a) {
        a.classList.remove("active");
      });
      if (active) active.link.classList.add("active");
    }

    window.addEventListener("scroll", onScroll, { passive: true });
    onScroll();
  }
})();
