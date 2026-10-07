"""Decode QR matrices produced by qr.js, proving they are actually scannable.

Module for module equality with a reference encoder says the bits are right;
this says a camera would read them. Reads JSON on stdin:

    [{"text": "...", "rows": ["0101...", ...]}, ...]

and writes the decoded payload (or null when the decoder found nothing) for
each entry as JSON on stdout.
"""
import json
import sys

import cv2
import numpy as np


def render(rows, scale=8, quiet=4):
    size = len(rows)
    side = (size + quiet * 2) * scale
    canvas = np.full((side, side), 255, dtype=np.uint8)
    for row_index, row in enumerate(rows):
        for col_index, cell in enumerate(row):
            if cell == "1":
                top = (row_index + quiet) * scale
                left = (col_index + quiet) * scale
                canvas[top:top + scale, left:left + scale] = 0
    return canvas


def main():
    payloads = json.loads(sys.stdin.read())
    detector = cv2.QRCodeDetector()

    decoded = []
    for entry in payloads:
        if entry is None or entry.get("rows") is None:
            decoded.append(None)
            continue
        image = render(entry["rows"])
        text, points, _ = detector.detectAndDecode(image)
        decoded.append(text if points is not None else None)

    json.dump(decoded, sys.stdout)


if __name__ == "__main__":
    main()
