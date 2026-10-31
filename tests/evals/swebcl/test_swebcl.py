import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import gen_taskset
import grade


class ParserTests(unittest.TestCase):
    def test_sympy_verbose_results(self):
        sample = """
test_ok ok
test_failure F
test_error E
test_expected_failure f
test_unexpected_pass X
test_skip s
test_spaced ... ok
"""
        got = grade.parse_test_results(sample)
        self.assertEqual(got["test_ok"], "ok")
        self.assertEqual(got["test_spaced"], "ok")
        self.assertEqual(got["test_failure"], "F")
        self.assertNotEqual(got["test_expected_failure"], "ok")

    def test_touched_files(self):
        patch = "diff --git a/sympy/a.py b/sympy/a.py\n--- a/sympy/a.py\n+++ b/sympy/a.py\n"
        self.assertEqual(grade.touched_files(patch), ["sympy/a.py"])

    def test_grade_reverts_test_patch(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / "bin").mkdir()
            (root / "tests").mkdir()
            (root / "bin" / "test").write_text(
                "print('test_new ok')\n", encoding="utf-8"
            )
            (root / "bin" / "test").chmod(0o755)
            (root / "tests" / "sample.py").write_text("base\n", encoding="utf-8")
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            subprocess.run(["git", "add", "."], cwd=root, check=True)
            subprocess.run(["git", "-c", "user.email=test@example.invalid", "-c",
                             "user.name=test", "commit", "-qm", "base"], cwd=root, check=True)
            base_commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
            (root / "tests" / "sample.py").write_text("model edit\n", encoding="utf-8")
            patch = """diff --git a/tests/sample.py b/tests/sample.py
--- a/tests/sample.py
+++ b/tests/sample.py
@@ -1 +1,2 @@
 base
+test
"""
            data = {"id": "synthetic", "test_patch": patch,
                    "base_commit": base_commit,
                    "FAIL_TO_PASS": ["test_new"], "PASS_TO_PASS": []}
            data_path = root / "data.json"
            data_path.write_text(json.dumps(data), encoding="utf-8")
            old = os.environ.get("SWEBCL_PYTHON")
            os.environ["SWEBCL_PYTHON"] = sys.executable
            try:
                old_cwd = pathlib.Path.cwd()
                os.chdir(root)
                try:
                    self.assertEqual(grade.grade(str(data_path)), 0)
                finally:
                    os.chdir(old_cwd)
            finally:
                if old is None:
                    os.environ.pop("SWEBCL_PYTHON", None)
                else:
                    os.environ["SWEBCL_PYTHON"] = old
            self.assertEqual((root / "tests" / "sample.py").read_text(), "base\n")


class GeneratorTests(unittest.TestCase):
    def test_generator_keeps_order_and_writes_setup(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            dataset = root / "dataset.json"
            fixture = root / "fixture"
            fixture.mkdir()
            tasks = []
            for index in range(2):
                tasks.append({
                    "metadata": {"instance_id": "sympy__x-%d" % index, "repo": "sympy/sympy",
                                 "base_commit": "abc%d" % index},
                    "task": {"problem_statement": "problem %d" % index},
                    "evaluation": {"patch": "gold", "test_patch": "test",
                                    "FAIL_TO_PASS": ["test_new"], "PASS_TO_PASS": []},
                })
            dataset.write_text(json.dumps({"sequences": [{"repo": "sympy/sympy", "tasks": tasks}]}))
            old = gen_taskset.DATASET
            gen_taskset.DATASET = dataset
            try:
                out = root / "out"
                ids = gen_taskset.generate("sympy/sympy", str(fixture), str(out))
            finally:
                gen_taskset.DATASET = old
            self.assertEqual(ids, ["sympy__x-0", "sympy__x-1"])
            first = json.loads((out / "tasks" / "001-sympy__x-0.json").read_text())
            second = json.loads((out / "tasks" / "002-sympy__x-1.json").read_text())
            first_data = json.loads((out / "data" / "sympy__x-0.json").read_text())
            self.assertEqual(first["phase"], "write")
            self.assertEqual(second["precursor"], "sympy__x-0")
            self.assertEqual(first_data["base_commit"], "abc0")
            self.assertIn("git checkout", first["setup"])


if __name__ == "__main__":
    unittest.main()
