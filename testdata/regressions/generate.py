#!/usr/bin/env python3
"""Regenerate the round-2 regression fixtures.

The fixtures are committed so the tests read the same bytes everywhere; this
script exists to reproduce them. Requires Pillow (the report's appendix used
the same tooling). Run from the repository root:

    python3 testdata/regressions/generate.py
"""
import struct
from pathlib import Path

from PIL import Image

OUT = Path("testdata/regressions")


def icc_profile(space: bytes) -> bytes:
    """A minimal ICC v2 header whose data colour space is `space`.

    AnyPic only reads the header's colour-space signature (bytes 16–20); it
    never interprets the profile. The tag table is empty, so this is not a
    usable colour transform — it exists to make the file's claim testable.
    """
    header = bytearray(128)
    struct.pack_into(">I", header, 0, 132)  # profile size: header + tag count
    header[4:8] = b"anyp"
    header[8:12] = b"\x02\x10\x00\x00"  # ICC v2.1
    header[12:16] = b"mntr"
    header[16:20] = space
    header[20:24] = b"XYZ "
    header[36:40] = b"acsp"
    header[64:68] = b"\x00\x00\x00\x00"  # rendering intent: perceptual
    return bytes(header) + b"\x00\x00\x00\x00"


def apng() -> None:
    frames = [
        Image.new("RGBA", (64, 64), (255, 0, 0, 255) if i % 2 == 0 else (0, 0, 255, 255))
        for i in range(6)
    ]
    frames[0].save(
        OUT / "apng-6frames.png",
        save_all=True,
        append_images=frames[1:],
        duration=100,
        loop=0,
    )


def cmyk_jpeg() -> None:
    im = Image.new("CMYK", (128, 128))
    px = im.load()
    for y in range(128):
        for x in range(128):
            px[x, y] = (x * 2 % 256, y * 2 % 256, 60, 20)
    im.save(OUT / "cmyk-icc.jpg", quality=90, icc_profile=icc_profile(b"CMYK"))


def gray_jpeg_with_rgb_icc() -> None:
    im = Image.new("L", (120, 90))
    px = im.load()
    for y in range(90):
        for x in range(120):
            px[x, y] = (x * 2 + y) % 256
    im.save(OUT / "gray-rgb-icc.jpg", quality=90, icc_profile=icc_profile(b"RGB "))


def renamed_gif() -> None:
    frames = [
        Image.new("RGB", (48, 48), (255, 0, 0) if i % 2 == 0 else (0, 0, 255))
        for i in range(5)
    ]
    # The bytes are a GIF; only the extension says PNG.
    frames[0].save(
        OUT / "renamed-gif.png",
        save_all=True,
        append_images=frames[1:],
        duration=80,
        loop=0,
        format="GIF",
    )


def webp_with_icc() -> None:
    im = Image.new("RGB", (64, 48))
    px = im.load()
    for y in range(48):
        for x in range(64):
            px[x, y] = (x * 4 % 256, y * 5 % 256, (x + y) * 3 % 256)
    im.save(OUT / "webp-icc.webp", quality=80, icc_profile=icc_profile(b"RGB "))


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    apng()
    cmyk_jpeg()
    gray_jpeg_with_rgb_icc()
    renamed_gif()
    webp_with_icc()
    for path in sorted(OUT.glob("*")):
        if path.is_file():
            print(f"{path.name}: {path.stat().st_size} bytes")


if __name__ == "__main__":
    main()
