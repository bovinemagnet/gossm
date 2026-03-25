"""Playwright tests for the gossm HTMX dashboard."""

import sys
from playwright.sync_api import sync_playwright, expect

BASE_URL = "http://localhost:18877"


def test_dashboard_loads(page):
    """Test that the dashboard loads and shows key elements."""
    page.goto(BASE_URL)
    page.wait_for_load_state("networkidle")

    # Title should contain gossm.
    assert "gossm" in page.title()

    # Header should show branding.
    header = page.locator("h1")
    expect(header).to_have_text("gossm")

    # Dashboard badge should be visible.
    badge = page.locator("text=dashboard")
    expect(badge).to_be_visible()

    # Port should be displayed.
    port_display = page.locator("text=18877")
    expect(port_display).to_be_visible()

    print("PASS: Dashboard loads correctly")


def test_stats_bar(page):
    """Test that the stats bar shows session count, uptime, and sparkline."""
    page.goto(BASE_URL)
    page.wait_for_load_state("networkidle")

    # Active Sessions card.
    sessions_label = page.locator("text=Active Sessions")
    expect(sessions_label).to_be_visible()

    # Session count should be 0.
    session_count = page.locator("#stats-bar .text-2xl.text-sky-400")
    expect(session_count).to_have_text("0")

    # Daemon Uptime card.
    uptime_label = page.locator("text=Daemon Uptime")
    expect(uptime_label).to_be_visible()

    # Session History card.
    history_label = page.locator("text=Session History")
    expect(history_label).to_be_visible()

    print("PASS: Stats bar displays correctly")


def test_sessions_table(page):
    """Test the sessions table structure."""
    page.goto(BASE_URL)
    page.wait_for_load_state("networkidle")

    # Sessions heading.
    heading = page.locator("h2:has-text('Sessions')")
    expect(heading).to_be_visible()

    # Table headers should be present.
    for header in ["Status", "Instance Name", "Instance ID", "Profile", "Type", "Ports", "Uptime", "Actions"]:
        th = page.locator(f"th:has-text('{header}')")
        expect(th).to_be_visible()

    # Empty state should show when no sessions.
    empty_state = page.locator("text=No active sessions")
    expect(empty_state).to_be_visible()

    # Refresh button should exist.
    refresh_btn = page.locator("text=Refresh")
    expect(refresh_btn).to_be_visible()

    print("PASS: Sessions table structure correct")


def test_new_session_form(page):
    """Test the new session form has all expected fields."""
    page.goto(BASE_URL)
    page.wait_for_load_state("networkidle")

    # Form heading.
    heading = page.locator("h2:has-text('Start New Session')")
    expect(heading).to_be_visible()

    # All form fields should be present.
    expect(page.locator("#instance_id")).to_be_visible()
    expect(page.locator("#instance_name")).to_be_visible()
    expect(page.locator("#profile")).to_be_visible()
    expect(page.locator("#type")).to_be_visible()
    expect(page.locator("#local_port")).to_be_visible()
    expect(page.locator("#remote_port")).to_be_visible()
    expect(page.locator("#remote_host")).to_be_visible()

    # Session type dropdown should have Shell and Port Forward options.
    type_select = page.locator("#type")
    options = type_select.locator("option").all()
    option_texts = [opt.text_content() for opt in options]
    assert "Shell" in option_texts, f"Expected 'Shell' in options, got {option_texts}"
    assert "Port Forward" in option_texts, f"Expected 'Port Forward' in options, got {option_texts}"

    # Start Session button.
    submit_btn = page.locator("button[type='submit']:has-text('Start Session')")
    expect(submit_btn).to_be_visible()

    print("PASS: New session form has all fields")


def test_static_assets(page):
    """Test that htmx.min.js is served correctly."""
    response = page.goto(f"{BASE_URL}/static/htmx.min.js")
    assert response.status == 200, f"Expected 200, got {response.status}"
    assert response.headers.get("content-type", "").startswith("text/javascript") or \
           response.headers.get("content-type", "").startswith("application/javascript") or \
           "javascript" in response.headers.get("content-type", ""), \
           f"Unexpected content-type: {response.headers.get('content-type')}"

    print("PASS: Static assets served correctly")


def test_api_sessions_endpoint(page):
    """Test the /api/sessions endpoint returns HTML partial."""
    response = page.goto(f"{BASE_URL}/api/sessions")
    assert response.status == 200, f"Expected 200, got {response.status}"

    print("PASS: API sessions endpoint returns 200")


def test_refresh_button(page):
    """Test that clicking Refresh updates the session table via HTMX."""
    page.goto(BASE_URL)
    page.wait_for_load_state("networkidle")

    # Click refresh button.
    refresh_btn = page.locator("text=Refresh")
    refresh_btn.click()

    # Wait for HTMX request to complete.
    page.wait_for_load_state("networkidle")

    # Table body should still exist (even if empty).
    tbody = page.locator("#session-table-body")
    expect(tbody).to_be_visible()

    print("PASS: Refresh button works via HTMX")


def test_sse_connection(page):
    """Test that the SSE connection is established."""
    page.goto(BASE_URL)
    page.wait_for_load_state("networkidle")

    # The main element should have the SSE attributes.
    main = page.locator("main[hx-ext='sse']")
    expect(main).to_be_visible()
    expect(main).to_have_attribute("sse-connect", "/events")

    print("PASS: SSE connection attributes present")


def test_screenshot(page):
    """Take a full-page screenshot for visual inspection."""
    page.goto(BASE_URL)
    page.wait_for_load_state("networkidle")

    # Wait a moment for Tailwind CSS to load from CDN.
    page.wait_for_timeout(2000)

    page.screenshot(path="/tmp/gossm-dashboard.png", full_page=True)
    print("PASS: Screenshot saved to /tmp/gossm-dashboard.png")


def main():
    failed = 0
    passed = 0

    tests = [
        test_dashboard_loads,
        test_stats_bar,
        test_sessions_table,
        test_new_session_form,
        test_static_assets,
        test_api_sessions_endpoint,
        test_refresh_button,
        test_sse_connection,
        test_screenshot,
    ]

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)

        for test_fn in tests:
            page = browser.new_page()
            try:
                test_fn(page)
                passed += 1
            except Exception as e:
                print(f"FAIL: {test_fn.__name__}: {e}")
                failed += 1
            finally:
                page.close()

        browser.close()

    print(f"\n{passed} passed, {failed} failed out of {len(tests)} tests")
    return 1 if failed > 0 else 0


if __name__ == "__main__":
    sys.exit(main())
