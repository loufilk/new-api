"""New API Dashboard route ownership contract (2026-09-04)."""

import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
CADDY = ROOT / "server" / "Caddyfile"


def named_block(text: str, marker: str) -> str:
    lines = text.splitlines()
    for index, line in enumerate(lines):
        if line.strip() != marker:
            continue
        indent = len(line) - len(line.lstrip(" \t"))
        body: list[str] = []
        for candidate in lines[index + 1 :]:
            if (
                candidate.strip() == "}"
                and len(candidate) - len(candidate.lstrip(" \t")) == indent
            ):
                return "\n".join(body)
            body.append(candidate)
    raise AssertionError(f"missing Caddy block: {marker}")


class DashboardRouteContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.caddy = CADDY.read_text(encoding="utf-8")
        cls.ui_matcher = named_block(cls.caddy, "@new_api_ui {")
        cls.ui_handler = named_block(cls.caddy, "handle @new_api_ui {")

    def test_dashboard_and_direct_console_paths_belong_to_new_api(self) -> None:
        expected = (
            "/dashboard",
            "/dashboard/*",
            "/keys",
            "/keys/*",
            "/usage-logs",
            "/usage-logs/*",
            "/wallet",
            "/wallet/*",
            "/profile",
            "/profile/*",
            "/support",
            "/support/*",
        )
        matcher_tokens = set(self.ui_matcher.split())
        for path in expected:
            self.assertIn(path, matcher_tokens)

    def test_auth_customer_chat_and_admin_ui_are_explicit(self) -> None:
        matcher_tokens = set(self.ui_matcher.split())
        for path in (
            "/sign-in",
            "/sign-up",
            "/forgot-password",
            "/oauth/*",
            "/otp",
            "/reset",
            "/subscriptions",
            "/redemption-codes",
            "/chat",
            "/chat2link",
            "/errors/*",
            "/user-agreement",
            "/privacy-policy",
            "/channels",
            "/users",
            "/system-settings",
        ):
            self.assertIn(path, matcher_tokens)

    def test_task_plugin_and_error_deep_links_belong_to_new_api(self) -> None:
        matcher_tokens = set(self.ui_matcher.split())
        for path in ("/task-plugins", "/task-plugins/*", "/401", "/403", "/404", "/500", "/503"):
            with self.subTest(path=path):
                self.assertIn(path, matcher_tokens)

    def test_ui_handler_proxies_new_api_and_sets_private_html_headers(self) -> None:
        self.assertIn("reverse_proxy http://127.0.0.1:3000", self.ui_handler)
        for header in (
            "header_up Host {host}",
            "header_up X-Forwarded-Host {host}",
            "header_up X-Forwarded-Proto https",
            'header Cache-Control "no-cache"',
            'header X-Robots-Tag "noindex, nofollow"',
        ):
            self.assertIn(header, self.ui_handler)

    def test_ui_handler_precedes_web_home_fallback(self) -> None:
        self.assertLess(
            self.caddy.index("handle @new_api_ui"),
            self.caddy.index("try_files {path}index.html"),
        )

    def test_static_assets_retain_prior_chunks_with_application_fallback(self) -> None:
        handler = named_block(self.caddy, "handle @new_api_static {")
        self.assertIn("root /opt/middle/new-api-static", handler)
        self.assertIn("pass_thru", handler)
        self.assertLess(handler.index("file_server"), handler.index("reverse_proxy"))
        self.assertIn('header Cache-Control "public, max-age=31536000, immutable"', handler)

    def test_legacy_redirects_and_split_dashboard_matcher_are_absent(self) -> None:
        for source in ("/keys*", "/usage-logs*", "/wallet*", "/profile*"):
            self.assertNotIn(f"redir {source}", self.caddy)
        self.assertNotIn("@new_api_admin_html", self.caddy)
        self.assertNotIn("@dashboard_html", self.caddy)

    def test_public_and_new_api_product_routes_remain_split(self) -> None:
        matcher_tokens = set(self.ui_matcher.split())
        for path in ("/", "/models", "/docs"):
            self.assertNotIn(path, matcher_tokens)
            self.assertNotIn(f"{path}/*", matcher_tokens)

        for path in ("/pricing", "/playground", "/image", "/model-monitor"):
            self.assertIn(path, matcher_tokens)
            self.assertIn(f"{path}/*", matcher_tokens)


if __name__ == "__main__":
    unittest.main()
