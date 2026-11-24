#!/usr/bin/env python3
"""Build static HTML documentation from README.md and docs/*.md into docs/."""

from __future__ import annotations

import html
import re
from pathlib import Path

import markdown
from pygments.formatters import HtmlFormatter

ROOT = Path(__file__).resolve().parent.parent
DOCS_DIR = ROOT / "docs"
ASSETS_DIR = DOCS_DIR / "assets"
REPO_URL = "https://github.com/rusmanplatd/goravel-supabase"

PAGES = [
    {
        "slug": "index",
        "source": ROOT / "README.md",
        "title": "Overview",
        "nav_title": "Overview",
        "description": "1:1 supabase-js compliant Supabase SDK for Go and Goravel.",
    },
    {
        "slug": "client",
        "source": DOCS_DIR / "client.md",
        "title": "Client & Goravel Facade",
        "nav_title": "Client & Facade",
        "description": "Root client setup, options, Goravel facades.Supabase(), named connections, and per-request scoping.",
    },
    {
        "slug": "auth",
        "source": DOCS_DIR / "auth.md",
        "title": "Auth (GoTrue)",
        "nav_title": "Auth (GoTrue)",
        "description": "Password, OTP, OAuth + PKCE, MFA, Passkeys, SSO, Web3, JWKS, and Admin API.",
    },
    {
        "slug": "postgrest",
        "source": DOCS_DIR / "postgrest.md",
        "title": "PostgREST",
        "nav_title": "PostgREST",
        "description": "Fluent SELECT, INSERT, UPSERT, UPDATE, DELETE, filters, full-text search, and RPC.",
    },
    {
        "slug": "storage",
        "source": DOCS_DIR / "storage.md",
        "title": "Storage",
        "nav_title": "Storage",
        "description": "Buckets, file uploads/downloads, image transforms, signed URLs, Analytics Iceberg, and Vectors.",
    },
    {
        "slug": "realtime",
        "source": DOCS_DIR / "realtime.md",
        "title": "Realtime",
        "nav_title": "Realtime",
        "description": "Phoenix Channels WebSocket Broadcast, Postgres Changes, Presence, and HTTP Broadcast.",
    },
    {
        "slug": "functions",
        "source": DOCS_DIR / "functions.md",
        "title": "Edge Functions",
        "nav_title": "Edge Functions",
        "description": "JSON, binary, MultipartBody uploads, SSE streaming, regional routing, and typed errors.",
    },
]

HTML_TEMPLATE = """<!DOCTYPE html>
<html lang="en" data-theme="dark">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>{page_title} — Goravel Supabase Docs</title>
  <meta name="description" content="{meta_description}" />
  <link rel="stylesheet" href="assets/style.css" />
  <script>
    (function () {{
      try {{
        var saved = localStorage.getItem("gsb-theme");
        if (saved === "light" || saved === "dark") {{
          document.documentElement.setAttribute("data-theme", saved);
        }}
      }} catch (e) {{}}
    }})();
  </script>
</head>
<body>
  <header class="topbar">
    <div class="topbar-inner">
      <div class="topbar-left">
        <button class="menu-btn" id="menu-toggle" type="button" aria-label="Toggle navigation">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <line x1="3" y1="6" x2="21" y2="6"></line>
            <line x1="3" y1="12" x2="21" y2="12"></line>
            <line x1="3" y1="18" x2="21" y2="18"></line>
          </svg>
        </button>
        <a class="brand" href="index.html">
          <span class="brand-badge">GS</span>
          <span class="brand-text">Goravel Supabase</span>
        </a>
        <span class="version-pill">Go SDK</span>
      </div>
      <div class="topbar-right">
        <div class="search-wrap">
          <input id="nav-search" type="search" placeholder="Filter pages..." aria-label="Filter documentation pages" />
        </div>
        <button class="icon-btn" id="theme-toggle" type="button" aria-label="Toggle color theme" title="Toggle light/dark theme">
          <svg class="icon-sun" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="5"></circle>
            <line x1="12" y1="1" x2="12" y2="3"></line>
            <line x1="12" y1="21" x2="12" y2="23"></line>
            <line x1="4.22" y1="4.22" x2="5.64" y2="5.64"></line>
            <line x1="18.36" y1="18.36" x2="19.78" y2="19.78"></line>
            <line x1="1" y1="12" x2="3" y2="12"></line>
            <line x1="21" y1="12" x2="23" y2="12"></line>
            <line x1="4.22" y1="19.78" x2="5.64" y2="18.36"></line>
            <line x1="18.36" y1="5.64" x2="19.78" y2="4.22"></line>
          </svg>
          <svg class="icon-moon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"></path>
          </svg>
        </button>
        <a class="repo-link" href="{repo_url}" target="_blank" rel="noopener noreferrer">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0 0 24 12c0-6.63-5.37-12-12-12z"/>
          </svg>
          <span>GitHub</span>
        </a>
      </div>
    </div>
  </header>

  <div class="layout">
    <aside class="sidebar" id="sidebar">
      <div class="sidebar-section">
        <div class="sidebar-heading">Documentation</div>
        <nav class="sidebar-nav" id="sidebar-nav">
          {sidebar_links}
        </nav>
      </div>
      <div class="sidebar-section sidebar-meta">
        <div class="sidebar-heading">Resources</div>
        <nav class="sidebar-nav">
          <a class="nav-link" href="{repo_url}/blob/master/sdk-compliance.yaml" target="_blank" rel="noopener noreferrer">SDK Compliance (257)</a>
          <a class="nav-link" href="{repo_url}/blob/master/LICENSE" target="_blank" rel="noopener noreferrer">MIT License</a>
        </nav>
      </div>
    </aside>

    <div class="sidebar-backdrop" id="sidebar-backdrop"></div>

    <main class="content">
      <article class="prose">
        {content_html}
      </article>
      {pager_html}
    </main>

    <aside class="toc" aria-label="Table of contents">
      <div class="toc-inner">
        <div class="toc-heading">On this page</div>
        {toc_html}
      </div>
    </aside>
  </div>

  <script src="assets/app.js"></script>
</body>
</html>
"""


def rewrite_links(raw_md: str, is_readme: bool) -> str:
    """Rewrite markdown relative links so they resolve cleanly in docs/*.html."""
    if is_readme:
        raw_md = re.sub(r"\(docs/([a-z0-9_-]+)\.md\)", r"(\1.html)", raw_md)
        raw_md = raw_md.replace(
            "(sdk-compliance.yaml)",
            f"({REPO_URL}/blob/master/sdk-compliance.yaml)",
        )
    else:
        raw_md = re.sub(r"\(\./([a-z0-9_-]+)\.md\)", r"(\1.html)", raw_md)
        raw_md = re.sub(r"\(([a-z0-9_-]+)\.md\)", r"(\1.html)", raw_md)
        raw_md = raw_md.replace("(../README.md)", "(index.html)")
    return raw_md


def build_sidebar(active_slug: str) -> str:
    links = []
    for page in PAGES:
        cls = "nav-link active" if page["slug"] == active_slug else "nav-link"
        links.append(
            f'<a class="{cls}" href="{page["slug"]}.html">{html.escape(page["nav_title"])}</a>'
        )
    return "\n          ".join(links)


def build_pager(index: int) -> str:
    prev_page = PAGES[index - 1] if index > 0 else None
    next_page = PAGES[index + 1] if index + 1 < len(PAGES) else None
    parts = ['<nav class="pager" aria-label="Pagination">']
    if prev_page:
        parts.append(
            f'<a class="pager-card pager-prev" href="{prev_page["slug"]}.html">'
            f'<span class="pager-label">Previous</span>'
            f'<span class="pager-title">&larr; {html.escape(prev_page["nav_title"])}</span>'
            f"</a>"
        )
    else:
        parts.append("<div></div>")
    if next_page:
        parts.append(
            f'<a class="pager-card pager-next" href="{next_page["slug"]}.html">'
            f'<span class="pager-label">Next</span>'
            f'<span class="pager-title">{html.escape(next_page["nav_title"])} &rarr;</span>'
            f"</a>"
        )
    parts.append("</nav>")
    return "\n".join(parts)


def write_assets() -> None:
    ASSETS_DIR.mkdir(parents=True, exist_ok=True)
    pygments_dark = HtmlFormatter(style="monokai").get_style_defs(
        '[data-theme="dark"] .codehilite'
    )
    pygments_light = HtmlFormatter(style="default").get_style_defs(
        '[data-theme="light"] .codehilite'
    )

    css = f""":root {{
  --bg: #0f1419;
  --bg-elev: #161d24;
  --bg-code: #11171d;
  --border: #26313d;
  --text: #e6edf3;
  --text-muted: #93a1b0;
  --accent: #3ecf8e;
  --accent-dim: rgba(62, 207, 142, 0.14);
  --link: #56d9a0;
  --topbar-h: 60px;
  --sidebar-w: 256px;
  --toc-w: 230px;
}}

[data-theme="light"] {{
  --bg: #ffffff;
  --bg-elev: #f6f8fa;
  --bg-code: #f6f8fa;
  --border: #d8dee4;
  --text: #1f2328;
  --text-muted: #59636e;
  --accent: #1a9e66;
  --accent-dim: rgba(26, 158, 102, 0.12);
  --link: #158053;
}}

* {{
  box-sizing: border-box;
}}

html {{
  scroll-behavior: smooth;
  scroll-padding-top: calc(var(--topbar-h) + 20px);
}}

body {{
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, Roboto, Helvetica, Arial, sans-serif;
  font-size: 15px;
  line-height: 1.65;
}}

a {{
  color: var(--link);
  text-decoration: none;
}}

a:hover {{
  text-decoration: underline;
}}

.topbar {{
  position: sticky;
  top: 0;
  z-index: 40;
  height: var(--topbar-h);
  background: var(--bg);
  border-bottom: 1px solid var(--border);
  backdrop-filter: blur(8px);
}}

.topbar-inner {{
  max-width: 1440px;
  margin: 0 auto;
  height: 100%;
  padding: 0 20px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}}

.topbar-left,
.topbar-right {{
  display: flex;
  align-items: center;
  gap: 12px;
}}

.menu-btn {{
  display: none;
  background: transparent;
  border: 1px solid var(--border);
  color: var(--text);
  border-radius: 6px;
  padding: 6px;
  cursor: pointer;
}}

.brand {{
  display: inline-flex;
  align-items: center;
  gap: 10px;
  color: var(--text);
  font-weight: 700;
  font-size: 16px;
}}

.brand:hover {{
  text-decoration: none;
}}

.brand-badge {{
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 7px;
  background: var(--accent);
  color: #092217;
  font-size: 13px;
  font-weight: 800;
  letter-spacing: -0.02em;
}}

.version-pill {{
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--accent-dim);
  color: var(--accent);
  border: 1px solid var(--accent-dim);
}}

.search-wrap input {{
  width: 190px;
  padding: 6px 10px;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: var(--bg-elev);
  color: var(--text);
  font-size: 13px;
  outline: none;
}}

.search-wrap input:focus {{
  border-color: var(--accent);
}}

.icon-btn,
.repo-link {{
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: var(--bg-elev);
  color: var(--text);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
}}

.icon-btn:hover,
.repo-link:hover {{
  border-color: var(--accent);
  text-decoration: none;
}}

[data-theme="dark"] .icon-moon,
[data-theme="light"] .icon-sun {{
  display: none;
}}

.layout {{
  max-width: 1440px;
  margin: 0 auto;
  display: grid;
  grid-template-columns: var(--sidebar-w) minmax(0, 1fr) var(--toc-w);
  gap: 24px;
  padding: 0 20px;
}}

.sidebar {{
  position: sticky;
  top: var(--topbar-h);
  height: calc(100vh - var(--topbar-h));
  overflow-y: auto;
  padding: 24px 12px 32px 0;
  border-right: 1px solid var(--border);
}}

.sidebar-section + .sidebar-section {{
  margin-top: 24px;
}}

.sidebar-heading {{
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted);
  padding: 0 10px 8px;
}}

.sidebar-nav {{
  display: flex;
  flex-direction: column;
  gap: 2px;
}}

.nav-link {{
  display: block;
  padding: 7px 10px;
  border-radius: 6px;
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 500;
}}

.nav-link:hover {{
  color: var(--text);
  background: var(--bg-elev);
  text-decoration: none;
}}

.nav-link.active {{
  color: var(--accent);
  background: var(--accent-dim);
  font-weight: 600;
}}

.nav-link.hidden {{
  display: none;
}}

.sidebar-backdrop {{
  display: none;
}}

.content {{
  min-width: 0;
  padding: 28px 16px 64px;
  max-width: 860px;
  width: 100%;
  margin: 0 auto;
}}

.prose h1 {{
  font-size: 32px;
  line-height: 1.25;
  margin: 0 0 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border);
}}

.prose h2 {{
  font-size: 22px;
  line-height: 1.3;
  margin: 36px 0 12px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--border);
}}

.prose h3 {{
  font-size: 17px;
  line-height: 1.4;
  margin: 24px 0 8px;
}}

.prose p {{
  margin: 0 0 14px;
}}

.prose ul,
.prose ol {{
  margin: 0 0 16px;
  padding-left: 22px;
}}

.prose li {{
  margin: 4px 0;
}}

.prose hr {{
  border: 0;
  border-top: 1px solid var(--border);
  margin: 28px 0;
}}

.prose blockquote {{
  margin: 16px 0;
  padding: 10px 16px;
  border-left: 3px solid var(--accent);
  background: var(--bg-elev);
  border-radius: 0 6px 6px 0;
  color: var(--text-muted);
}}

.prose code {{
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 0.875em;
  background: var(--bg-elev);
  padding: 2px 5px;
  border-radius: 4px;
  border: 1px solid var(--border);
}}

.prose pre {{
  margin: 0;
  padding: 14px 16px;
  overflow-x: auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 13px;
  line-height: 1.55;
}}

.prose pre code {{
  background: transparent;
  border: 0;
  padding: 0;
  font-size: inherit;
}}

.codehilite {{
  position: relative;
  margin: 16px 0;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--bg-code) !important;
  overflow: hidden;
}}

.copy-btn {{
  position: absolute;
  top: 8px;
  right: 8px;
  padding: 4px 9px;
  font-size: 11px;
  font-weight: 600;
  border-radius: 5px;
  border: 1px solid var(--border);
  background: var(--bg-elev);
  color: var(--text-muted);
  cursor: pointer;
  opacity: 0.85;
  z-index: 2;
}}

.copy-btn:hover {{
  opacity: 1;
  color: var(--text);
  border-color: var(--accent);
}}

.prose table {{
  width: 100%;
  border-collapse: collapse;
  margin: 18px 0;
  font-size: 14px;
  display: block;
  overflow-x: auto;
}}

.prose th,
.prose td {{
  border: 1px solid var(--border);
  padding: 9px 12px;
  text-align: left;
  vertical-align: top;
}}

.prose th {{
  background: var(--bg-elev);
  font-weight: 600;
}}

.pager {{
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
  margin-top: 44px;
  padding-top: 24px;
  border-top: 1px solid var(--border);
}}

.pager-card {{
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 14px 16px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--bg-elev);
  color: var(--text);
}}

.pager-card:hover {{
  border-color: var(--accent);
  text-decoration: none;
}}

.pager-next {{
  text-align: right;
}}

.pager-label {{
  font-size: 12px;
  color: var(--text-muted);
}}

.pager-title {{
  font-size: 14px;
  font-weight: 600;
  color: var(--accent);
}}

.toc {{
  position: sticky;
  top: var(--topbar-h);
  height: calc(100vh - var(--topbar-h));
  overflow-y: auto;
  padding: 28px 0 32px 12px;
}}

.toc-heading {{
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted);
  margin-bottom: 10px;
}}

.toc ul {{
  list-style: none;
  margin: 0;
  padding: 0;
}}

.toc ul ul {{
  padding-left: 12px;
}}

.toc li {{
  margin: 4px 0;
}}

.toc a {{
  display: block;
  font-size: 12.5px;
  color: var(--text-muted);
  line-height: 1.4;
  padding: 2px 0;
}}

.toc a:hover,
.toc a.active {{
  color: var(--accent);
  text-decoration: none;
}}

@media (max-width: 1120px) {{
  .layout {{
    grid-template-columns: var(--sidebar-w) minmax(0, 1fr);
  }}
  .toc {{
    display: none;
  }}
}}

@media (max-width: 820px) {{
  .menu-btn {{
    display: inline-flex;
  }}
  .search-wrap {{
    display: none;
  }}
  .layout {{
    grid-template-columns: minmax(0, 1fr);
  }}
  .sidebar {{
    position: fixed;
    top: var(--topbar-h);
    left: 0;
    width: var(--sidebar-w);
    background: var(--bg);
    z-index: 50;
    padding: 20px 14px;
    transform: translateX(-100%);
    transition: transform 0.2s ease;
  }}
  .sidebar.open {{
    transform: translateX(0);
  }}
  .sidebar-backdrop.open {{
    display: block;
    position: fixed;
    inset: var(--topbar-h) 0 0 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 45;
  }}
  .pager {{
    grid-template-columns: 1fr;
  }}
}}

{pygments_dark}

{pygments_light}
"""

    js = """(function () {
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
"""

    (ASSETS_DIR / "style.css").write_text(css, encoding="utf-8")
    (ASSETS_DIR / "app.js").write_text(js, encoding="utf-8")


def main() -> None:
    write_assets()
    (DOCS_DIR / ".nojekyll").write_text("", encoding="utf-8")

    for idx, page in enumerate(PAGES):
        raw_md = page["source"].read_text(encoding="utf-8")
        processed_md = rewrite_links(raw_md, is_readme=(page["slug"] == "index"))

        md = markdown.Markdown(
            extensions=[
                "fenced_code",
                "codehilite",
                "tables",
                "toc",
                "sane_lists",
            ],
            extension_configs={
                "codehilite": {"guess_lang": False, "css_class": "codehilite"},
                "toc": {"permalink": False, "toc_depth": "2-3"},
            },
        )
        content_html = md.convert(processed_md)
        toc_html = md.toc or ""

        rendered = HTML_TEMPLATE.format(
            page_title=html.escape(page["title"]),
            meta_description=html.escape(page["description"]),
            repo_url=REPO_URL,
            sidebar_links=build_sidebar(page["slug"]),
            content_html=content_html,
            pager_html=build_pager(idx),
            toc_html=toc_html,
        )
        out_path = DOCS_DIR / f'{page["slug"]}.html'
        out_path.write_text(rendered, encoding="utf-8")
        print(f"Generated {out_path.relative_to(ROOT)}")


if __name__ == "__main__":
    main()
