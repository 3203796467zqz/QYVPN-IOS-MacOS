#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-or-later
"""把 macOS 客户端源码、脚本与已编译内核打成一份可直接拷到 Mac 上出包的 zip。

在 Windows/Linux 上准备发布时使用（Mac 上直接 ./build.sh 即可，不需要这个脚本）。
打包时保留可执行权限，供 build.sh 与内核二进制使用。

用法：
    python3 tools/make-bundle.py [--version 1.0.0]
"""
import argparse
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SKIP_DIRS = {".build", "__pycache__", ".swiftpm"}
SKIP_SUFFIXES = {".pyc"}
# dist 只带预编译内核；其余产物（.app、zip）由 Mac 上重新生成。
ALLOW_DIST = ("engine/qyvpn-engine",)


def is_executable(rel: Path) -> bool:
    """Windows 上文件没有可执行位，按名字判断。"""
    name = rel.name
    if name.endswith((".sh", ".py", ".command")):
        return True
    return name == "qyvpn-engine" or name.startswith("qyvpn-engine-")


def add(zf: zipfile.ZipFile, path: Path, arcname: str) -> None:
    info = zipfile.ZipInfo.from_file(path, arcname)
    info.date_time = (2026, 1, 1, 0, 0, 0)
    mode = 0o755 if is_executable(path.relative_to(ROOT)) else 0o644
    info.external_attr = (mode & 0xFFFF) << 16
    info.compress_type = zipfile.ZIP_DEFLATED
    zf.writestr(info, path.read_bytes())


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", default="1.0.0")
    args = parser.parse_args()

    out_dir = ROOT / "dist"
    out_dir.mkdir(parents=True, exist_ok=True)
    target = out_dir / f"清幽私人云_macOS_v{args.version}_构建包.zip"

    count = 0
    with zipfile.ZipFile(target, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
        for path in sorted(ROOT.rglob("*")):
            rel = path.relative_to(ROOT)
            if any(part in SKIP_DIRS for part in rel.parts):
                continue
            if rel.parts[0] == "dist":
                keep = "/".join(rel.parts[1:])
                if not any(keep == allow or keep.startswith(allow) for allow in ALLOW_DIST):
                    continue
            if path.suffix in SKIP_SUFFIXES or not path.is_file():
                continue
            add(zf, path, str(Path("macos") / rel))
            count += 1

    size = target.stat().st_size
    print(f"生成 {target}")
    print(f"  文件数 {count}，大小 {size:,} 字节")


if __name__ == "__main__":
    main()
