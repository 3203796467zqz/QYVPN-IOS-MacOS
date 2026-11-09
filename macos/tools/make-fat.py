#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-or-later
"""把两个单架构 Mach-O 合并成 macOS 通用二进制（等价于 lipo -create）。

在没有 Mac 的机器上预先产出 universal 内核时使用；格式为 fat Mach-O：
    fat_header  { magic(0xCAFEBABE, 大端), nfat_arch }
    fat_arch[]  { cputype, cpusubtype, offset, size, align }（全部大端）
每个切片按 2^align（这里统一 16384）对齐。

用法：
    python3 tools/make-fat.py dist/engine/qyvpn-engine-arm64 \
                              dist/engine/qyvpn-engine-amd64 \
                              -o dist/engine/qyvpn-engine
"""
import argparse
import struct
import sys
from pathlib import Path

FAT_MAGIC = 0xCAFEBABE
MH_MAGIC_64 = 0xFEEDFACF
ALIGN = 14  # 2^14 = 16384，满足 arm64 的对齐要求


def read_thin(path: Path):
    data = path.read_bytes()
    magic, cputype, cpusubtype, filetype = struct.unpack_from("<IIII", data, 0)
    if magic != MH_MAGIC_64:
        sys.exit(f"{path} 不是 64 位 Mach-O")
    if filetype != 2:
        sys.exit(f"{path} 不是可执行文件（filetype={filetype}）")
    return data, cputype, cpusubtype


def align_up(value: int, alignment: int) -> int:
    mask = alignment - 1
    return (value + mask) & ~mask


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("inputs", nargs="+", type=Path)
    parser.add_argument("-o", "--output", required=True, type=Path)
    args = parser.parse_args()

    slices = []
    for path in args.inputs:
        data, cputype, cpusubtype = read_thin(path)
        slices.append((cputype, cpusubtype, data, path))

    header_size = 8 + 20 * len(slices)
    offset = align_up(header_size, 1 << ALIGN)

    header = struct.pack(">II", FAT_MAGIC, len(slices))
    body = bytearray()
    for cputype, cpusubtype, data, _path in slices:
        header += struct.pack(">IIIII", cputype, cpusubtype, offset, len(data), ALIGN)
        pad = offset - header_size - len(body)
        body += b"\x00" * pad
        body += data
        offset = align_up(offset + len(data), 1 << ALIGN)

    out = header + bytes(body)
    args.output.write_bytes(out)

    # 回读校验：切片的字节必须与输入完全一致。
    raw = args.output.read_bytes()
    magic, count = struct.unpack_from(">II", raw, 0)
    assert magic == FAT_MAGIC and count == len(slices), "fat header 写入异常"
    for i, (cputype, cpusubtype, data, path) in enumerate(slices):
        ct, cs, off, size, align = struct.unpack_from(">IIIII", raw, 8 + 20 * i)
        assert (ct, cs, off, size, align) == (cputype, cpusubtype, off, len(data), ALIGN)
        assert raw[off:off + size] == data, f"{path} 切片内容不一致"
        print(f"  {path.name:26s} cputype=0x{ct:08x} offset={off:>8d} size={size:>9d}")

    print(f"生成 {args.output}（{len(out):,} 字节，{len(slices)} 个架构）")


if __name__ == "__main__":
    main()
