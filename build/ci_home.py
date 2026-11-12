"""Create and remove only the home recorded by this workflow's setup step."""
import json
import os
from pathlib import Path
import re
import shutil
import stat
import sys
import tempfile


FIELDS = {"root", "root_dev", "root_ino", "home", "home_dev", "home_ino"}


def directory_identity(path):
    info = path.lstat()
    # Windows junctions are reparse points too, not ordinary directories.
    if not stat.S_ISDIR(info.st_mode) or getattr(info, "st_file_attributes", 0) & 0x400:
        raise ValueError("refusing a link, reparse point, or non-directory")
    return info.st_dev, info.st_ino


def create_home(runner_temp):
    root = Path(runner_temp).resolve(strict=True)
    root_dev, root_ino = directory_identity(root)
    home = Path(tempfile.mkdtemp(prefix="magpie-ci-home.", dir=root))
    home_dev, home_ino = directory_identity(home)
    return dict(root=str(root), root_dev=root_dev, root_ino=root_ino,
                home=str(home), home_dev=home_dev, home_ino=home_ino)



def make_removable(path):
    """Only called on the detached, identity-checked home and its children."""
    info = path.lstat()
    if stat.S_ISLNK(info.st_mode) or getattr(info, "st_file_attributes", 0) & 0x400:
        return  # Never chmod or descend through a link/junction.
    if stat.S_ISDIR(info.st_mode):
        if sys.platform == "win32":
            # Windows rmdir also refuses a readonly directory. This changes
            # only its readonly attribute, after the link/reparse guard above.
            if not info.st_mode & stat.S_IWRITE:
                os.chmod(path, stat.S_IMODE(info.st_mode) | stat.S_IWRITE)
        else:
            os.chmod(path, stat.S_IMODE(info.st_mode) | stat.S_IRWXU, follow_symlinks=False)
        for child in path.iterdir():
            make_removable(child)
    elif stat.S_ISREG(info.st_mode) and sys.platform == "win32" and not info.st_mode & stat.S_IWRITE:
        if info.st_nlink != 1:
            raise ValueError("refusing to change attributes of a readonly hard-linked file")
        # Python 3.12 on Windows uses Get/SetFileAttributesW (the link itself,
        # not its target); its chmod does not accept follow_symlinks=False.
        # POSIX does not need file chmod to unlink, so leave its inodes alone.
        os.chmod(path, stat.S_IMODE(info.st_mode) | stat.S_IWRITE)


def remove_home(runner_temp, record):
    if not isinstance(record, dict) or set(record) != FIELDS:
        raise ValueError("missing or invalid creation record")
    for key in ("root_dev", "root_ino", "home_dev", "home_ino"):
        if type(record[key]) is not int:
            raise ValueError("invalid recorded directory identity")
    root = Path(runner_temp).resolve(strict=True)
    if str(root) != record["root"] or directory_identity(root) != (record["root_dev"], record["root_ino"]):
        raise ValueError("runner temporary directory changed")
    home = Path(record["home"])
    if home.parent != root or not re.fullmatch(r"magpie-ci-home\.[a-zA-Z0-9_-]{8,32}", home.name):
        raise ValueError("home is not a direct generated child of RUNNER_TEMP")
    expected = record["home_dev"], record["home_ino"]
    if directory_identity(home) != expected:
        raise ValueError("recorded home was replaced")
    # Reserve a fresh sibling name, then detach and recheck before any chmod.
    # Staying under the same parent also allows a readonly home to be renamed.
    # Release the empty reservation because Windows cannot rename over it.
    quarantine = Path(tempfile.mkdtemp(prefix=".magpie-ci-cleanup.", dir=root))
    quarantine.rmdir()
    home.rename(quarantine)
    if directory_identity(quarantine) != expected:
        raise ValueError("home changed during cleanup; preserved for inspection")
    make_removable(quarantine)
    shutil.rmtree(quarantine)


def main():
    runner_temp = os.environ["RUNNER_TEMP"]
    if len(sys.argv) != 2 or sys.argv[1] not in ("create", "cleanup"):
        raise ValueError("expected create or cleanup")
    if sys.argv[1] == "cleanup":
        remove_home(runner_temp, json.loads(os.environ["MAGPIE_CI_HOME_RECORD"]))
        return
    # Resolve output paths before creating anything. The saved step output,
    # not HOME or another mutable test variable, supplies cleanup's identity.
    output, environment = os.environ["GITHUB_OUTPUT"], os.environ["GITHUB_ENV"]
    record = create_home(runner_temp)
    home = Path(record["home"])
    with open(output, "a", encoding="utf-8") as stream:
        print("record=" + json.dumps(record, separators=(",", ":")), file=stream)
    folders = {"HOME": home, "USERPROFILE": home,
               "XDG_CONFIG_HOME": home / ".config", "XDG_CACHE_HOME": home / ".cache",
               "XDG_DATA_HOME": home / ".local/share", "XDG_STATE_HOME": home / ".local/state",
               "APPDATA": home / "AppData/Roaming", "LOCALAPPDATA": home / "AppData/Local"}
    with open(environment, "a", encoding="utf-8") as stream:
        for name, path in folders.items():
            print(f"{name}={path}", file=stream)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError) as error:
        print(f"ci-home: {error}", file=sys.stderr)
        sys.exit(1)
