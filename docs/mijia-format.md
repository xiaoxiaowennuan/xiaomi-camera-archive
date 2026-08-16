# Xiaomi/Mijia recording format notes

This document describes the media layout currently recognized by Xiaomi Camera Archive. The findings came from a read-only inspection of a Xiaomi/Mijia camera archive. Dataset-specific dates, counts, paths, and filenames have been removed from the public documentation.

## Verified layout

An archive folder contains the following relevant directories:

```text
MIJIA_RECORD_VIDEO/
  YYYYMMDDHH/
    mmMssS_UNIX.mp4
    mmMssS_UNIX.jpeg
MIJIA_RECORD_MOTION/              # optional
  YYYYMMDDHH/
    motion_record_msg
log/                             # ignored
```

The indexer applies these rules:

- `YYYYMMDDHH` is a 10-digit local-time hour directory.
- `mmMssS_UNIX.mp4` contains two-digit minute and second fields followed by a 10-digit Unix timestamp.
- The Unix timestamp is the segment start time. The hour directory, minute, and second must agree with it in the configured timezone.
- A same-basename `.jpeg` file is an optional thumbnail.
- Symlinks, non-regular files, hidden metadata, `.cached` files, unknown extensions, and unrelated files are ignored.
- A configured archive folder must contain `MIJIA_RECORD_VIDEO`; `MIJIA_RECORD_MOTION` is optional.

Synthetic example for `Asia/Shanghai`:

```text
MIJIA_RECORD_VIDEO/2024010203/04M05S_1704135845.mp4
MIJIA_RECORD_VIDEO/2024010203/04M05S_1704135845.jpeg
```

## Observed media properties

Representative MP4 files from the inspected archive used:

- MP4/QuickTime container with an `hvc1` video tag.
- HEVC Main video at 2560×1440 and either 10 or 20 fps.
- PCM A-law mono audio at 16 kHz.
- Segments of approximately one minute.
- I-frames approximately every three seconds.
- 864×480 JFIF JPEG thumbnails.

These values describe the inspected format, not a guarantee for every Xiaomi/Mijia model. Every new or changed MP4 is probed with `ffprobe`; an unsupported or damaged file is marked unavailable without aborting the complete scan.

HEVC plus PCM A-law is not consistently playable across browsers. The player first attempts the source file and can generate an H.264/AAC compatibility copy in the separate writable state directory after a playback compatibility failure. Original media is never modified.

## Motion metadata

Observed `motion_record_msg` files use this layout:

```text
128-byte header beginning with ASCII IMIEVENT
N × 32-byte records
```

When a record is viewed as eight little-endian `uint32` values:

- Field 1 is the Unix timestamp of a matching video segment.
- Field 2 was zero in the inspected data.
- Field 3 was either zero or one; remaining fields were zero.

Based on observed co-occurrence, the original application labels, and user verification, this project maps field 3 value `0` to frame change and value `1` to person movement. This interpretation is evidence-based but is not an official binary format specification.

Multiple motion records may refer to the same segment start. The indexer deduplicates them by timestamp, preserves the raw record count, and retains whether value `1` appeared. It does not invent an event duration from unknown fields.

## Unsupported metadata

Some archives contain encrypted or wrapped log files with `IMI/` and `v1.0.0` headers, as well as an unrelated `.record_msg` file. Their semantics are unknown, so the application neither decrypts nor indexes them.

## Conservative indexing rules

1. Index only `MIJIA_RECORD_VIDEO/YYYYMMDDHH/mmMssS_UNIX.mp4` and an optional same-basename JPEG.
2. Treat the Unix timestamp as the segment start and reject conflicting time fields.
3. Ignore symlinks, hidden files, `.cached` files, unknown extensions, and non-regular files.
4. Probe only new or changed MP4 files based on relative path, size, and mtime.
5. Aggregate motion records by Unix timestamp and associate them with indexed segments.
6. Keep source media, SQLite state, transcode cache, and temporary files in separate trust boundaries.

## Unknowns

- Other camera generations may use different codecs, resolutions, directory names, or motion flags.
- The motion flag mapping is not backed by a vendor binary format specification.
- `.record_msg` and encrypted log semantics remain unknown and intentionally unsupported.
