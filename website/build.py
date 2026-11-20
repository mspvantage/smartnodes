#!/usr/bin/env python3
"""Builds the SmartNodes website.

Each file in pages/ is one page's main content. Its first lines are
settings, then a blank line, then the HTML:

    title: Run a node
    description: How to run an entry, exit or bridge node.
    nav: run-a-node

This script wraps every page in the shared header and footer and writes the
finished site to site/. It needs only Python 3, nothing to install:

    python3 build.py

The site uses no outside services: no fonts, scripts, analytics or images
from other sites, so visiting it tells nobody else anything.
"""
import html
import pathlib

DOMAIN = "https://smartnodes.tech"
REPO = "https://github.com/mspvantage/smartnodes"

# The public test network's details. Until it launches, the guides show
# these placeholders and a notice; at launch, fill them in and rebuild.
NETWORK = {
    "launched": False,
    "chain_id": "smartnodes-test-1",
    "genesis_url": "https://smartnodes.tech/testnet/genesis.json",
    "genesis_sha256": "PUBLISHED_AT_LAUNCH",
    "seeds": "PUBLISHED_AT_LAUNCH",
    "rpc": "PUBLISHED_AT_LAUNCH",
    "faucet": "PUBLISHED_AT_LAUNCH",
    "issuer_key_id": "0",
    "min_gas_price": "0.025usnode",
}

NAV = [
    ("index", "Overview"),
    ("how-it-works", "How it works"),
    ("install", "Install"),
    ("use", "Use the VPN"),
    ("run-a-node", "Run a node"),
    ("validator", "Run a validator"),
    ("build", "Build"),
    ("tokenomics", "Token (draft)"),
    ("security", "Security"),
]

HERE = pathlib.Path(__file__).parent
LOGO = (HERE / "logo.svg").read_text().strip()

LAYOUT = """<!doctype html>
<html lang="en" data-bs-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<meta http-equiv="Content-Security-Policy" content="default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'">
<title>{title}</title>
<meta name="description" content="{description}">
<link rel="canonical" href="{domain}/{file}">
<link rel="icon" href="assets/favicon.svg" type="image/svg+xml">
<link rel="stylesheet" href="assets/tabler.min.css">
<link rel="stylesheet" href="assets/site.css">
</head>
<body>
<div class="page">
<header class="navbar navbar-expand-lg sticky-top sn-navbar">
  <div class="container-xl">
    <button class="navbar-toggler" type="button" data-bs-toggle="collapse" data-bs-target="#nav" aria-controls="nav" aria-expanded="false" aria-label="Menu">
      <span class="navbar-toggler-icon"></span>
    </button>
    <a href="index.html" class="navbar-brand sn-brand">{logo}<span>SmartNodes</span></a>
    <div class="navbar-nav flex-row order-lg-last">
      <a class="btn btn-outline-primary btn-sm" href="{repo}">Source code</a>
    </div>
    <div class="collapse navbar-collapse" id="nav">
      <ul class="navbar-nav">
{nav}
      </ul>
    </div>
  </div>
</header>
<div class="sn-status">
  <div class="container-xl">Early test network: unaudited, and nothing on it has real value. <a href="security.html#status">What this means</a></div>
</div>
<main class="page-wrapper">
{body}
</main>
<footer class="footer footer-transparent d-print-none">
  <div class="container-xl">
    <div class="row text-center align-items-center">
      <div class="col-12 col-lg-auto mb-3 mb-lg-0">
        SmartNodes is free, open-source software. This site sets no cookies and loads nothing from other sites.
      </div>
      <div class="col-lg-auto ms-lg-auto">
        <ul class="list-inline list-inline-dots mb-0">
          <li class="list-inline-item"><a href="{repo}" class="link-secondary">GitHub</a></li>
          <li class="list-inline-item"><a href="security.html#report" class="link-secondary">Report a security issue</a></li>
          <li class="list-inline-item"><a href="tokenomics.html#notice" class="link-secondary">Token notice</a></li>
        </ul>
      </div>
    </div>
  </div>
</footer>
</div>
<script src="assets/tabler.min.js"></script>
</body>
</html>
"""


def nav_html(active):
    items = []
    for slug, label in NAV:
        cls = "nav-item active" if slug == active else "nav-item"
        current = ' aria-current="page"' if slug == active else ""
        items.append(f'        <li class="{cls}"><a class="nav-link" href="{slug}.html"{current}><span class="nav-link-title">{label}</span></a></li>')
    return "\n".join(items)


def build():
    out = HERE / "site"
    out.mkdir(exist_ok=True)
    for page in sorted((HERE / "pages").glob("*.html")):
        head, body = page.read_text().split("\n\n", 1)
        meta = dict(line.split(": ", 1) for line in head.splitlines())
        name = page.stem + ".html"
        title = meta["title"] if page.stem == "index" else f"{meta['title']} · SmartNodes"
        text = LAYOUT.format(
            title=html.escape(title), description=html.escape(meta["description"]),
            domain=DOMAIN, file="" if page.stem == "index" else name, logo=LOGO,
            repo=REPO, nav=nav_html(meta.get("nav", page.stem)), body=body.replace("{repo}", REPO),
        )
        for key, value in NETWORK.items():
            text = text.replace("{net:" + key + "}", html.escape(str(value)))
        if "{net:notice}" in text:
            notice = "" if NETWORK["launched"] else (
                '<div class="alert alert-warning"><b>The public test network has not launched yet.</b> '
                'Values shown as <code>PUBLISHED_AT_LAUNCH</code> will be filled in here when it does. '
                'You can practise every step now on a <a href="build.html#local">local test chain</a>.</div>')
            text = text.replace("{net:notice}", notice)
        assert "{net:" not in text, f"{name}: unknown network value"
        (out / name).write_text(text)
        print("wrote", out / name)


if __name__ == "__main__":
    build()
