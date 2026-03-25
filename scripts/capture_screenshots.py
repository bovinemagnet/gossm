"""Capture gossm dashboard screenshots for Antora documentation."""

import argparse
import sys
from pathlib import Path

from playwright.sync_api import sync_playwright


DEFAULT_URL = "http://localhost:8877"
DEFAULT_OUTPUT = "src/docs/modules/ROOT/images"

SCREENSHOTS = [
    {
        "name": "dashboard-full.png",
        "selector": None,
        "description": "Full dashboard page",
    },
    {
        "name": "dashboard-stats.png",
        "selector": "#stats-bar",
        "description": "Stats bar",
    },
    {
        "name": "dashboard-sessions.png",
        "selector": "#sessions-section",
        "description": "Sessions table",
    },
    {
        "name": "dashboard-new-session.png",
        "selector": "#new-session-form",
        "description": "New session form",
    },
    {
        "name": "dashboard-presets.png",
        "selector": "#presets-section",
        "description": "Saved session presets",
    },
]


def capture(url: str, output_dir: Path) -> int:
    """Capture all screenshots. Returns 0 on success, 1 on failure."""
    output_dir.mkdir(parents=True, exist_ok=True)
    captured = []

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page(
            viewport={"width": 1280, "height": 900},
            device_scale_factor=2,
        )

        try:
            page.goto(url, timeout=5000, wait_until="networkidle")
        except Exception as e:
            print(f"ERROR: Could not connect to {url}: {e}", file=sys.stderr)
            print("Is the gossm daemon running?", file=sys.stderr)
            browser.close()
            return 1

        # Wait for Tailwind CDN to load and render styles.
        page.wait_for_timeout(2000)

        for shot in SCREENSHOTS:
            path = output_dir / shot["name"]

            if shot["selector"] is None:
                # Full-page screenshot.
                page.screenshot(path=str(path), full_page=True)
                captured.append(shot)
                print(f"  Captured: {shot['description']} -> {path}")
                continue

            element = page.locator(shot["selector"])
            if element.count() == 0:
                print(f"  Skipped:  {shot['description']} (element not found)")
                continue

            element.screenshot(path=str(path))
            captured.append(shot)
            print(f"  Captured: {shot['description']} -> {path}")

        browser.close()

    print(f"\n{len(captured)} of {len(SCREENSHOTS)} screenshots captured.")
    return 0


def main():
    parser = argparse.ArgumentParser(
        description="Capture gossm dashboard screenshots for documentation."
    )
    parser.add_argument(
        "--url",
        default=DEFAULT_URL,
        help=f"Dashboard URL (default: {DEFAULT_URL})",
    )
    parser.add_argument(
        "--output-dir",
        default=DEFAULT_OUTPUT,
        help=f"Output directory (default: {DEFAULT_OUTPUT})",
    )
    args = parser.parse_args()

    print(f"Capturing dashboard screenshots from {args.url}")
    sys.exit(capture(args.url, Path(args.output_dir)))


if __name__ == "__main__":
    main()
