"""im.19 identities are independent of the legacy cutover allowlist."""
from copy import deepcopy
import importlib.util
from pathlib import Path
import unittest

from release_identity import STOCK_REVISION, VERSIONS

SPEC = importlib.util.spec_from_file_location("im19_pins", Path(__file__).with_name("check-im19-image-pins.py"))
PREFLIGHT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PREFLIGHT)

PREV = ("fixture/newapi@sha256:" + "a" * 64, "b" * 40)
CANDIDATE = ("fixture/newapi@sha256:" + "c" * 64, "d" * 40)
KEY = "org.opencontainers.image.version"


def image(pin, rev, iid, version):
    return {"Id": "sha256:" + iid * 64, "Os": "linux", "Architecture": "amd64", "RepoDigests": [pin],
            "Config": {"Labels": {KEY: version, "org.opencontainers.image.revision": rev}}}


class TestIm19Identity(unittest.TestCase):
    def setUp(self):
        self.images = {
            PREV[0]: image(*PREV, "e", VERSIONS["target"]),
            CANDIDATE[0]: image(*CANDIDATE, "f", PREFLIGHT.IM19_VERSION + "+g" + CANDIDATE[1][:12]),
        }

    def test_separately_pinned_roles(self):
        artifacts = PREFLIGHT.verify_staged_pins(PREV, CANDIDATE, "amd64", self.images.__getitem__)
        self.assertEqual(artifacts["candidate"]["label_revision"], CANDIDATE[1])
        self.assertEqual(artifacts["previous"]["label_revision"], PREV[1])

    def test_rejects_ineligible_pins_before_inspect(self):
        bad = [((PREV[0], STOCK_REVISION), CANDIDATE), (PREV, PREV),
               (PREV, ("fixture/newapi:latest", CANDIDATE[1])),
               (PREV, ("sha256:" + "c" * 64, CANDIDATE[1])),
               (PREV, (CANDIDATE[0], CANDIDATE[1][:12]))]
        for prev, candidate in bad:
            with self.subTest(candidate=candidate, prev=prev), self.assertRaises(ValueError):
                PREFLIGHT.verify_staged_pins(prev, candidate, "amd64", lambda _: self.fail("must not inspect"))

    def test_rejects_wrong_version_digest_role_revision_or_platform(self):
        original = self.images[CANDIDATE[0]]
        for field, value in (("Architecture", "arm64"), ("RepoDigests", [PREV[0]]),
                             ("Id", self.images[PREV[0]]["Id"]),
                             (KEY, VERSIONS["target"] + "+g" + CANDIDATE[1][:12]),
                             (KEY, PREFLIGHT.IM19_VERSION),
                             (KEY, PREFLIGHT.IM19_VERSION + "+g" + PREV[1][:12]),
                             ("org.opencontainers.image.revision", PREV[1]),
                             ("io.chuanapi.release.mode", "im13-schema-preserving-rollback")):
            actual = deepcopy(original)
            fields = actual if field in actual else actual["Config"]["Labels"]
            fields[field] = value
            self.images[CANDIDATE[0]] = actual
            with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                PREFLIGHT.verify_staged_pins(PREV, CANDIDATE, "amd64", self.images.__getitem__)


if __name__ == "__main__":
    unittest.main()
