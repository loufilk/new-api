"""Exercise the branch publish command without Docker or registry access."""

import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = ROOT / ".github/workflows/docker-image-branch.yml"


class BranchManifestTests(unittest.TestCase):
    def test_each_manifest_uses_one_run_even_if_public_tags_move(self):
        for kind in ("branch", "versioned"):
            with self.subTest(kind=kind):
                self.assert_manifest_sources(kind)

    def assert_manifest_sources(self, kind):
        workflow = WORKFLOW.read_text()
        block = workflow.split(f"      - name: Create & push manifest (Docker Hub - {kind})\n", 1)[1]
        block = block.split("      - name:", 1)[0]
        script = textwrap.dedent(block.split("        run: |\n", 1)[1])
        script = script.replace("${{ needs.prepare.outputs.tag_prefix }}", "nightly")
        script = script.replace("${{ needs.prepare.outputs.version }}", "nightly-20261024-aaaaaaa")
        build_tag = "build-" + "a" * 40 + "-12345-1"
        script = script.replace("${{ needs.prepare.outputs.build_tag }}", build_tag)
        # Another run (including a same-source rebuild) moved the public tags.
        revisions = {
            "calciumion/new-api:nightly-amd64": "commit-a",
            "calciumion/new-api:nightly-arm64": "commit-b",
            "calciumion/new-api:nightly-20261024-aaaaaaa-amd64": "commit-a",
            "calciumion/new-api:nightly-20261024-aaaaaaa-arm64": "another-build",
            f"calciumion/new-api:{build_tag}-amd64": "this-build",
            f"calciumion/new-api:{build_tag}-arm64": "this-build",
        }
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            docker = root / "docker"
            docker.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$DOCKER_ARGS"\n')
            docker.chmod(0o755)
            result = subprocess.run(["bash", "-eu", "-c", script], text=True, capture_output=True,
                                    env={**os.environ, "PATH": f"{root}:{os.environ['PATH']}",
                                         "DOCKER_ARGS": str(root / "args")}, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            args = (root / "args").read_text().splitlines()
        target = "nightly" if kind == "branch" else "nightly-20261024-aaaaaaa"
        self.assertEqual(args[:5], ["buildx", "imagetools", "create", "-t", f"calciumion/new-api:{target}"])
        self.assertEqual([revisions[source] for source in args[5:]], ["this-build", "this-build"])
        self.assertEqual(len(args[5:]), 2)

    def test_internal_source_tag_is_unique_per_run_and_retry_despite_branch_normalization(self):
        workflow = WORKFLOW.read_text()
        block = workflow.split("      - name: Resolve Docker tags\n", 1)[1].split("\n  build_single_arch:", 1)[0]
        script = textwrap.dedent(block.split("        run: |\n", 1)[1])
        build_tags = []
        for branch, run_id, attempt in [("feature/a", "100", "1"), ("feature-a", "101", "1"), ("feature/a", "100", "2")]:
            with tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "outputs"
                result = subprocess.run(["bash", "-eu", "-c", script], cwd=ROOT, text=True, capture_output=True,
                                        env={**os.environ, "BRANCH_NAME": branch, "GITHUB_RUN_ID": run_id,
                                             "GITHUB_RUN_ATTEMPT": attempt, "GITHUB_OUTPUT": str(output)}, timeout=10)
                self.assertEqual(result.returncode, 0, result.stderr)
                outputs = dict(line.split("=", 1) for line in output.read_text().splitlines())
            self.assertEqual(outputs["tag_prefix"], "feature-a")
            self.assertIn("build_tag", outputs)
            self.assertRegex(outputs["build_tag"], rf"^build-[0-9a-f]{{40}}-{run_id}-{attempt}$")
            self.assertLessEqual(len(outputs["build_tag"] + "-amd64"), 128)
            build_tags.append(outputs["build_tag"])
        self.assertEqual(len(set(build_tags)), 3)
        self.assertIn("build_tag: ${{ steps.version.outputs.build_tag }}", workflow)
        self.assertIn("calciumion/new-api:${{ needs.prepare.outputs.build_tag }}-${{ matrix.arch }}", workflow)

    def test_same_branch_publishes_are_serialized_without_cancelling_half_a_build(self):
        workflow = WORKFLOW.read_text()
        self.assertIn("concurrency:\n  group: docker-image-branch-${{ inputs.branch }}\n  cancel-in-progress: false", workflow)


if __name__ == "__main__":
    unittest.main()
