"""Synthetic filesystem regressions; never inspect or clean the user's HOME."""
import importlib.util
import json
import stat
from types import SimpleNamespace
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("ci_home", Path(__file__).with_name("ci_home.py"))
ci_home = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ci_home)


class HomeCleanupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="magpie-ci-home-tests.")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.record = ci_home.create_home(self.root)
        self.home = Path(self.record["home"])
        self.outside = self.root / "unrelated-home"
        self.outside.mkdir()
        self.sentinel = self.outside / "keep"
        self.sentinel.write_text("synthetic unrelated content", encoding="utf-8")

    def assert_untouched(self):
        self.assertEqual(self.sentinel.read_text(encoding="utf-8"), "synthetic unrelated content")

    def test_removes_only_recorded_home(self):
        (self.home / "test-data").write_text("synthetic", encoding="utf-8")
        # A changed HOME must not select the directory that cleanup removes.
        with patch.dict(ci_home.os.environ, {"HOME": str(self.outside)}):
            ci_home.remove_home(self.root, self.record)
        self.assertFalse(self.home.exists())
        self.assert_untouched()

    def test_rejects_wrong_prefix(self):
        dev, ino = ci_home.directory_identity(self.outside)
        bad = dict(self.record, home=str(self.outside), home_dev=dev, home_ino=ino)
        with self.assertRaises(ValueError):
            ci_home.remove_home(self.root, bad)
        self.assert_untouched()
        self.assertTrue(self.home.is_dir())

    def test_rejects_other_root(self):
        with self.assertRaises(ValueError):
            ci_home.remove_home(self.outside, self.record)
        self.assert_untouched()
        self.assertTrue(self.home.is_dir())

    def test_rejects_replaced_directory(self):
        original = self.root / "saved-original"
        self.home.rename(original)
        self.home.mkdir()
        marker = self.home / "replacement-data"
        marker.write_text("keep", encoding="utf-8")
        with self.assertRaises(ValueError):
            ci_home.remove_home(self.root, self.record)
        self.assertEqual(marker.read_text(encoding="utf-8"), "keep")
        self.assertTrue(original.is_dir())
        self.assert_untouched()

    def test_rejects_root_symlink(self):
        original = self.root / "saved-original"
        self.home.rename(original)
        self.home.symlink_to(self.outside, target_is_directory=True)
        with self.assertRaises(ValueError):
            ci_home.remove_home(self.root, self.record)
        self.assertTrue(self.home.is_symlink())
        self.assert_untouched()

    def test_rejects_reparse_point(self):
        lstat = Path.lstat

        def with_reparse_flag(path, *args, **kwargs):
            info = lstat(path, *args, **kwargs)
            if path == self.home:
                return SimpleNamespace(st_mode=info.st_mode, st_dev=info.st_dev,
                                       st_ino=info.st_ino, st_file_attributes=0x400)
            return info

        with patch.object(Path, "lstat", with_reparse_flag):
            with self.assertRaises(ValueError):
                ci_home.remove_home(self.root, self.record)
        self.assertTrue(self.home.is_dir())
        self.assert_untouched()

    def test_creation_output_supplies_cleanup_identity(self):
        output, environment = self.root / "output", self.root / "environment"
        variables = {"RUNNER_TEMP": str(self.root), "GITHUB_OUTPUT": str(output),
                     "GITHUB_ENV": str(environment)}
        with patch.dict(ci_home.os.environ, variables), patch.object(ci_home.sys, "argv", ["ci_home", "create"]):
            ci_home.main()
        key, value = output.read_text(encoding="utf-8").strip().split("=", 1)
        self.assertEqual(key, "record")
        created = Path(json.loads(value)["home"])
        self.assertIn("HOME=" + str(created), environment.read_text(encoding="utf-8").splitlines())
        variables.update(MAGPIE_CI_HOME_RECORD=value, HOME=str(self.outside))
        with patch.dict(ci_home.os.environ, variables), patch.object(ci_home.sys, "argv", ["ci_home", "cleanup"]):
            ci_home.main()
        self.assertFalse(created.exists())
        self.assertTrue(self.home.is_dir())
        self.assert_untouched()

    def test_does_not_follow_child_symlink(self):
        (self.home / "external-link").symlink_to(self.outside, target_is_directory=True)
        ci_home.remove_home(self.root, self.record)
        self.assertFalse(self.home.exists())
        self.assert_untouched()

    def test_rechecks_identity_after_move(self):
        rename = Path.rename
        saved = self.root / "saved-original"

        def replace_before_move(source, destination):
            if source == self.home:
                rename(source, saved)
                source.mkdir()
                (source / "replacement-data").write_text("keep", encoding="utf-8")
            return rename(source, destination)

        with patch.object(Path, "rename", replace_before_move):
            with self.assertRaises(ValueError):
                ci_home.remove_home(self.root, self.record)
        preserved = list(self.root.glob(".magpie-ci-cleanup.*/replacement-data"))
        self.assertEqual(len(preserved), 1)
        self.assertEqual(preserved[0].read_text(encoding="utf-8"), "keep")
        self.assertTrue(saved.is_dir())
        self.assert_untouched()

    def test_removes_readonly_directories_without_changing_link_target(self):
        nested = self.home / "readonly-directory"
        nested.mkdir()
        (nested / "file").write_text("synthetic", encoding="utf-8")
        (nested / "outside-link").symlink_to(self.outside, target_is_directory=True)
        self.outside.chmod(0o500)
        self.sentinel.chmod(0o400)
        outside_mode = stat.S_IMODE(self.outside.stat().st_mode)
        sentinel_mode = stat.S_IMODE(self.sentinel.stat().st_mode)
        nested.chmod(0o500)
        self.home.chmod(0o500)
        try:
            ci_home.remove_home(self.root, self.record)
        except PermissionError as error:
            self.fail(f"owned readonly directories must be removable: {error}")
        self.assertFalse(self.home.exists())
        self.assertEqual(stat.S_IMODE(self.outside.stat().st_mode), outside_mode)
        self.assertEqual(stat.S_IMODE(self.sentinel.stat().st_mode), sentinel_mode)
        self.assert_untouched()

    def test_removes_readonly_file(self):
        readonly = self.home / "readonly-file"
        readonly.write_text("synthetic", encoding="utf-8")
        readonly.chmod(stat.S_IREAD)
        ci_home.remove_home(self.root, self.record)
        self.assertFalse(self.home.exists())
        self.assert_untouched()

    def test_windows_readonly_directories_require_write_bit(self):
        nested = self.home / "readonly-directory"
        nested.mkdir()
        (nested / "file").write_text("synthetic", encoding="utf-8")
        (nested / "outside-link").symlink_to(self.outside, target_is_directory=True)
        self.outside.chmod(0o500)
        self.sentinel.chmod(0o400)
        outside_mode = stat.S_IMODE(self.outside.stat().st_mode)
        sentinel_mode = stat.S_IMODE(self.sentinel.stat().st_mode)
        nested.chmod(0o500)
        self.home.chmod(0o500)
        rmtree = ci_home.shutil.rmtree

        def windows_remove(path):
            for directory in (path, path / "readonly-directory"):
                self.assertTrue(directory.stat().st_mode & stat.S_IWRITE,
                                "Windows requires clearing the readonly directory attribute")
            self.assertEqual(stat.S_IMODE(self.outside.stat().st_mode), outside_mode)
            self.assertEqual(stat.S_IMODE(self.sentinel.stat().st_mode), sentinel_mode)
            rmtree(path)

        # Exercise the Windows branch on POSIX too. The unmocked readonly
        # directory regression remains the native Windows acceptance check.
        with patch.object(ci_home.sys, "platform", "win32"), patch.object(ci_home.shutil, "rmtree", windows_remove):
            ci_home.remove_home(self.root, self.record)
        self.assertFalse(self.home.exists())
        self.assertEqual(stat.S_IMODE(self.outside.stat().st_mode), outside_mode)
        self.assertEqual(stat.S_IMODE(self.sentinel.stat().st_mode), sentinel_mode)
        self.assert_untouched()

    def test_windows_readonly_file_requires_write_bit(self):
        readonly = self.home / "readonly-file"
        readonly.write_text("synthetic", encoding="utf-8")
        readonly.chmod(stat.S_IREAD)
        rmtree = ci_home.shutil.rmtree

        def windows_remove(path):
            moved_file = path / "readonly-file"
            self.assertTrue(moved_file.stat().st_mode & stat.S_IWRITE,
                            "Windows requires clearing the readonly file attribute")
            rmtree(path)

        # Exercise the Windows branch even on POSIX. Native Windows execution
        # of test_removes_readonly_file remains the platform acceptance check.
        with patch.object(ci_home.sys, "platform", "win32"), patch.object(ci_home.shutil, "rmtree", windows_remove):
            ci_home.remove_home(self.root, self.record)
        self.assertFalse(self.home.exists())
        self.assert_untouched()

    def test_windows_readonly_hardlink_is_not_chmodded(self):
        self.sentinel.chmod(stat.S_IREAD)
        linked = self.home / "hardlink"
        ci_home.os.link(self.sentinel, linked)
        before = stat.S_IMODE(self.sentinel.stat().st_mode)
        with patch.object(ci_home.sys, "platform", "win32"):
            with self.assertRaisesRegex(ValueError, "hard-linked"):
                ci_home.remove_home(self.root, self.record)
        self.assertEqual(stat.S_IMODE(self.sentinel.stat().st_mode), before)
        self.assert_untouched()

    def test_rejects_missing_record(self):
        with self.assertRaises(ValueError):
            ci_home.remove_home(self.root, {})
        self.assertTrue(self.home.is_dir())
        self.assert_untouched()


if __name__ == "__main__":
    unittest.main()
