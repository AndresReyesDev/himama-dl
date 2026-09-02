# himama-dl

An unofficial bulk downloader for [Lillio](https://www.lillio.com) (formerly HiMama) childcare activities and daily reports.

This scrapes your child's activities and reports through the website, and it's likely to be quite brittle as Lillio changes their site.

## Features

- Downloads all activity photos and videos, organized into folders by date
- Downloads daily report PDFs (only completed "View Report" — in-progress "Preview Report" is skipped)
- Supports multiple children — each child gets their own folder
- Hidden password input (no echo)
- Idempotent — re-running skips files that already exist, only downloads new ones

## Installation

```
go install github.com/AndresReyesDev/himama-dl@latest
```

## Usage

1. Run `himama-dl`; it will prompt for your Lillio credentials
2. Select which child to download data for (or press enter if only one child is found)
3. Wait

### CLI flags

```
himama-dl [-username email] [-password password] [-output dir]
```

- `-username` — Lillio username (your email). If omitted, you'll be prompted.
- `-password` — Lillio password. If omitted, you'll be prompted (hidden input).
- `-output` — Output directory (default: `output`)

## Output structure

```
output/
  Child Name (1234567)/
    Activities/
      2026-08-31/
        2026-08-31 - Classroom - Activity Title - a1b2c3d4.jpg
        2026-08-31 - Teacher Name - Another Activity - e5f6g7h8.mov
      2026-09-01/
        2026-09-01 - Teacher Name - Activity Title - 1a2b3c4d.jpeg
        ...
    Reports/
      2026-08-31.pdf
      2026-09-01.pdf
```

Each child gets a folder named `Name (AccountID)`. Activities are grouped into subfolders by date, with each file named by date, author, title, and a short hash for uniqueness. Reports are saved as `{date}.pdf`.


