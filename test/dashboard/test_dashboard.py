"""Playwright test to verify the gossm dashboard renders correctly."""

from playwright.sync_api import sync_playwright

URL = "http://localhost:18877"

def test_dashboard():
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()

        # Load the dashboard
        page.goto(URL)
        page.wait_for_load_state("networkidle")

        # Take a screenshot for visual inspection
        page.screenshot(path="/tmp/gossm_dashboard.png", full_page=True)

        # --- Header checks ---
        header = page.locator("header")
        assert header.is_visible(), "Header should be visible"

        title = page.locator("h1")
        assert title.inner_text() == "gossm", f"Title should be 'gossm', got '{title.inner_text()}'"

        badge = page.locator("header span.text-xs")
        assert badge.inner_text() == "dashboard", "Dashboard badge should say 'dashboard'"

        port_display = page.locator("header .text-sm")
        assert "8877" in port_display.inner_text(), "Port 8877 should be displayed in header"

        print("[PASS] Header: title, badge, and port display correct")

        # --- Stats bar checks ---
        stats_bar = page.locator("#stats-bar")
        assert stats_bar.is_visible(), "Stats bar should be visible"

        # Active Sessions card
        active_sessions = page.locator("text=Active Sessions")
        assert active_sessions.is_visible(), "Active Sessions label should be visible"

        session_count = page.locator(".text-sky-400.text-2xl")
        assert session_count.inner_text() == "0", f"Session count should be 0, got '{session_count.inner_text()}'"

        # Daemon Uptime card
        uptime_label = page.locator("text=Daemon Uptime")
        assert uptime_label.is_visible(), "Daemon Uptime label should be visible"

        # Session History card
        history_label = page.locator("text=Session History")
        assert history_label.is_visible(), "Session History label should be visible"

        print("[PASS] Stats bar: all three cards present and correct")

        # --- Sessions table checks ---
        sessions_section = page.locator("#sessions-section")
        assert sessions_section.is_visible(), "Sessions section should be visible"

        sessions_heading = page.locator("#sessions-section h2")
        assert sessions_heading.inner_text() == "Sessions", "Sessions heading should say 'Sessions'"

        refresh_btn = page.locator("#sessions-section button", has_text="Refresh")
        assert refresh_btn.is_visible(), "Refresh button should be visible"

        # Table headers
        headers = page.locator("#sessions-section thead th")
        expected_headers = ["Status", "Instance Name", "Instance ID", "Profile", "Type", "Ports", "Uptime", "Actions"]
        header_texts = [h.inner_text() for h in headers.all()]
        assert header_texts == expected_headers, f"Table headers mismatch: {header_texts}"

        # Empty state message
        empty_msg = page.locator("#session-table-body td")
        assert "No active sessions" in empty_msg.inner_text(), "Should show empty state message"

        print("[PASS] Sessions table: headers, refresh button, and empty state correct")

        # --- New session form checks ---
        form = page.locator("#new-session-form")
        assert form.is_visible(), "New session form should be visible"

        form_heading = page.locator("text=Start New Session")
        assert form_heading.is_visible(), "Form heading should say 'Start New Session'"

        # Check all form fields exist
        profile_input = page.locator("#profile")
        assert profile_input.is_visible(), "Profile input should be visible"

        filter_input = page.locator("#filter")
        assert filter_input.is_visible(), "Filter input should be visible"

        instance_id = page.locator("#instance_id")
        assert instance_id.is_visible(), "Instance ID field should be visible"
        assert instance_id.get_attribute("readonly") is not None, "Instance ID should be readonly"

        instance_name = page.locator("#instance_name")
        assert instance_name.is_visible(), "Instance Name field should be visible"
        assert instance_name.get_attribute("readonly") is not None, "Instance Name should be readonly"

        type_select = page.locator("#type")
        assert type_select.is_visible(), "Type select should be visible"
        options = type_select.locator("option")
        option_values = [o.get_attribute("value") for o in options.all()]
        assert option_values == ["shell", "port-forward"], f"Type options mismatch: {option_values}"

        local_port = page.locator("#local_port")
        assert local_port.is_visible(), "Local port input should be visible"

        remote_port = page.locator("#remote_port")
        assert remote_port.is_visible(), "Remote port input should be visible"

        remote_host = page.locator("#remote_host")
        assert remote_host.is_visible(), "Remote host input should be visible"

        submit_btn = page.locator("button[type='submit']")
        assert submit_btn.is_visible(), "Submit button should be visible"
        assert submit_btn.inner_text() == "Start Session", "Submit button should say 'Start Session'"

        print("[PASS] New session form: all fields and controls present")

        # --- Instance picker placeholder ---
        picker = page.locator("#instance-picker")
        assert picker.is_visible(), "Instance picker should be visible"
        assert "Enter an AWS profile" in picker.inner_text(), "Picker should show profile prompt"

        print("[PASS] Instance picker: shows default prompt")

        # --- No Saved Sessions section (no presets configured) ---
        saved_sessions = page.locator("text=Saved Sessions")
        assert saved_sessions.count() == 0, "Saved Sessions should not appear when no presets configured"

        print("[PASS] Presets: correctly hidden when none configured")

        # --- SSE connection ---
        main_el = page.locator("main[hx-ext='sse']")
        assert main_el.is_visible(), "Main element with SSE should be present"
        assert main_el.get_attribute("sse-connect") == "/events", "SSE should connect to /events"

        print("[PASS] SSE: connection endpoint configured")

        # --- Static assets loaded ---
        # Check that htmx is loaded by evaluating JS
        htmx_loaded = page.evaluate("typeof htmx !== 'undefined'")
        assert htmx_loaded, "htmx should be loaded"

        print("[PASS] Static assets: htmx loaded successfully")

        print("\n=== ALL DASHBOARD TESTS PASSED ===")

        browser.close()

if __name__ == "__main__":
    test_dashboard()
