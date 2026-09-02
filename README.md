# himama-dl

An unofficial bulk downloader for [Lillio](https://www.lillio.com) (formerly HiMama) childcare activities and daily reports.

Based on [meagar/himama-dl](https://github.com/meagar/himama-dl), with additional features including daily report downloads, date-organized folder structure, `.env` support, verbose mode, and improved error handling.

This scrapes your child's activities and reports through the website, and it's likely to be quite brittle as Lillio changes their site.

## Features

- Downloads all activity photos and videos, organized into folders by date
- Downloads daily reports as self-contained HTML files with media linked to downloaded activity files (only completed "View Report" — in-progress "Preview Report" is skipped)
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
himama-dl [-username email] [-password password] [-output dir] [-v]
```

- `-username` — Lillio username (your email). If omitted, falls back to `.env` or prompt.
- `-password` — Lillio password. If omitted, falls back to `.env` or prompt (hidden input).
- `-output` — Output directory (default: `output`)
- `-v` — Verbose output (shows URLs, file paths, raw dates, page counts)

### .env file

To avoid typing credentials every time, create a `.env` file in the directory where you run `himama-dl`:

```
cp .env.example .env
```

Edit `.env` with your credentials:

```
HIMAMA_USERNAME=your-email@example.com
HIMAMA_PASSWORD=your-password
```

Flags override `.env` values, and `.env` overrides the interactive prompt.

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
      2026-08-31.html
      2026-09-01.html
```

Each child gets a folder named `Name (AccountID)`. Activities are grouped into subfolders by date, with each file named by date, author, title, and a short hash for uniqueness. Reports are saved as `{date}.html` with media URLs rewritten to point to the downloaded activity files.

## Testing

```
go test ./...
```

Tests cover HTML parsing logic (CSRF token extraction, activity row parsing with malformed input handling).

## Disclaimer

This project is not affiliated with, endorsed by, or sponsored by Lillio (formerly HiMama). It is an unofficial tool that scrapes the Lillio website using authenticated HTTP requests. Use at your own risk — the scraping logic may break at any time if Lillio changes their website structure. Do not use this tool to download data for children other than your own. The authors of this tool are not responsible for any misuse or consequences arising from its use.

This project is licensed under the [MIT License](LICENSE).
