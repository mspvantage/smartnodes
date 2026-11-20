# The SmartNodes website

Plain static pages, built with [Tabler](https://tabler.io) (MIT licence),
for https://smartnodes.tech. Nothing is loaded from other sites: no fonts,
scripts, analytics or cookies.

- `pages/`: each page's content. Edit these.
- `build.py`: the shared header, menu and footer. Run `python3 build.py`
  after any edit; it rewrites `site/`.
- `site/`: the finished site, which is what gets published. Commit it.
- `logo.svg`: the logo used in the menu.

Publishing is done by `.github/workflows/website.yml`: every push to `main`
that changes `website/` publishes `website/site/` to GitHub Pages.
