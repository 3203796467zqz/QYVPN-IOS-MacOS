#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-or-later
"""用 App 现有图标生成 macOS 应用图标（AppIcon.icns）与界面内小图标（AppLogo.png）。

图标源与 Android 客户端完全一致：app/src/main/res/drawable-nodpi/qingyou_logo.png
（Android 的 android:icon 指向 qingyou_launcher，其前景就是这张图）。

macOS 的图标规范与 Android 不同，所以这里只做形状适配、不改画面：
完整 logo 缩小、四角按 macOS 的圆角比例裁切、四周留标准透明边距，得到 Dock/Finder 图标。
窗口内的品牌标识直接复用这个 icns（不再单独生成小图，避免出现第二种画风）。

重新生成：
    python3 tools/make-icon.py
产物：app/Resources/AppIcon.icns、AppIcon-1024.png
"""
from pathlib import Path

from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parent.parent
REPO = ROOT.parent
SOURCE = REPO / "app" / "src" / "main" / "res" / "drawable-nodpi" / "qingyou_logo.png"
OUT_DIR = ROOT / "app" / "Resources"

SIZE = 1024
MARGIN_RATIO = 0.06      # macOS 图标四周留白（Big Sur 之后的常规比例）
CORNER_RATIO = 0.225     # 圆角半径 / 图标边长
SUPERSAMPLE = 4


def rounded_tile(image: Image.Image, size: int) -> Image.Image:
    """把方形图缩到 size，并裁成 macOS 风格的圆角方块（带透明边距）。"""
    s = size * SUPERSAMPLE
    canvas = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    margin = int(s * MARGIN_RATIO)
    tile = s - margin * 2
    artwork = image.convert("RGBA").resize((tile, tile), Image.LANCZOS)

    mask = Image.new("L", (tile, tile), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        [0, 0, tile - 1, tile - 1], radius=int(tile * CORNER_RATIO), fill=255
    )
    canvas.paste(artwork, (margin, margin), mask)
    return canvas.resize((size, size), Image.LANCZOS)


def main() -> None:
    if not SOURCE.exists():
        raise SystemExit(f"找不到图标源文件：{SOURCE}")
    OUT_DIR.mkdir(parents=True, exist_ok=True)

    source = Image.open(SOURCE)
    master = rounded_tile(source, SIZE)
    master.save(OUT_DIR / "AppIcon-1024.png", optimize=True)

    # ICNS 的 PNG 类型：icp4=16, icp5=32, ic07=128, ic08=256, ic09=512,
    # ic10=1024, ic11=32(@2x), ic12=64(@2x), ic13=256(@2x), ic14=512(@2x)
    entries = [
        (b"icp4", 16),
        (b"icp5", 32),
        (b"ic11", 32),
        (b"ic12", 64),
        (b"ic07", 128),
        (b"ic13", 256),
        (b"ic14", 512),
        (b"ic10", 1024),
    ]

    chunks = bytearray()
    for kind, size in entries:
        tmp = OUT_DIR / f".tmp-{kind.decode()}.png"
        master.resize((size, size), Image.LANCZOS).save(tmp, format="PNG", optimize=True)
        data = tmp.read_bytes()
        tmp.unlink()
        # ICNS 的块长度包含 8 字节块头（类型 + 长度字段本身）
        chunks += kind + (8 + len(data)).to_bytes(4, "big") + data

    icns = b"icns" + (8 + len(chunks)).to_bytes(4, "big") + bytes(chunks)
    (OUT_DIR / "AppIcon.icns").write_bytes(icns)
    print(f"图标源：{SOURCE.relative_to(REPO)}")
    print(f"生成 {OUT_DIR / 'AppIcon.icns'}（{len(icns):,} 字节）")
    print(f"生成 {OUT_DIR / 'AppIcon-1024.png'}")


if __name__ == "__main__":
    main()
