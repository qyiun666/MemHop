"""Time expression normalization for MemHop encoder preprocessing.

Replaces relative time expressions (e.g. "3 days ago", "yesterday")
with absolute date strings (e.g. "2026-05-20") before encoding.

This improves recall consistency: "the meeting 3 days ago" and
"the meeting on 2026-05-20" produce similar encodings.

Dependencies:
    - ``dateparser`` (optional, via pip). Falls back to regex-based
      replacement when unavailable.
"""

from __future__ import annotations

import re
from datetime import datetime, timedelta
from typing import Optional


def normalize_time(text: str, reference_date: Optional[datetime] = None) -> str:
    """Replace relative time expressions with absolute date strings.

    Args:
        text: Input text that may contain relative time expressions.
        reference_date: Reference date for computing absolute dates.
            Defaults to today.

    Returns:
        Text with relative expressions replaced by "YYYY-MM-DD" strings.
        If no expressions are found, returns the original text unchanged.

    Examples:
        >>> normalize_time("meeting 3 days ago")
        'meeting 2026-05-20'
        >>> normalize_time("yesterday was fun")
        '2026-05-22 was fun'
        >>> normalize_time("no time reference")
        'no time reference'
    """
    if not text:
        return text

    ref = reference_date or datetime.now()

    # Try dateparser first (more comprehensive)
    try:
        return _normalize_with_dateparser(text, ref)
    except ImportError:
        pass

    # Fallback: regex-based normalization
    return _normalize_regex(text, ref)


# ── dateparser-based normalization ─────────────────────────────

_DATE_RE = re.compile(
    r"\b("
    r"\d+\s+(days?|hours?|minutes?|weeks?|months?|years?)\s+ago"
    r"|yesterday"
    r"|today"
    r"|tomorrow"
    r"|last\s+(night|week|month|year|monday|tuesday|wednesday|thursday|friday|saturday|sunday)"
    r"|next\s+(week|month|year|monday|tuesday|wednesday|thursday|friday|saturday|sunday)"
    r"|a\s+(day|week|month|year)\s+ago"
    r"|an\s+(hour|day|week|month)\s+ago"
    r")\b",
    re.IGNORECASE,
)


def _normalize_with_dateparser(text: str, ref: datetime) -> str:
    import dateparser  # type: ignore[import-untyped]

    settings = {
        "RELATIVE_BASE": ref,
        "PREFER_DATES_FROM": "past",
        "RETURN_AS_TIMEZONE_AWARE": False,
    }

    def _replace(match: re.Match) -> str:
        expr = match.group(0)
        parsed = dateparser.parse(expr, settings=settings)
        if parsed:
            return parsed.strftime("%Y-%m-%d")
        return expr

    return _DATE_RE.sub(_replace, text)


# ── Regex fallback ──────────────────────────────────────────────

_DAY_NAMES = {
    "monday": 0, "tuesday": 1, "wednesday": 2, "thursday": 3,
    "friday": 4, "saturday": 5, "sunday": 6,
}

_RELATIVE_UNITS: dict[str, str] = {
    "days": "days", "day": "days",
    "hours": "hours", "hour": "hours",
    "minutes": "minutes", "minute": "minutes",
    "weeks": "weeks", "week": "weeks",
    "months": "months", "month": "months",  # approximate
    "years": "years", "year": "years",       # approximate
}

_AGO_RE = re.compile(r"(\d+)\s+(days?|hours?|minutes?|weeks?|months?|years?)\s+ago", re.IGNORECASE)
_SIMPLE_RE = re.compile(
    r"\b(yesterday|today|tomorrow)\b", re.IGNORECASE
)
_LAST_RE = re.compile(r"last\s+(night|week|month|year)", re.IGNORECASE)


def _normalize_regex(text: str, ref: datetime) -> str:
    """Simple regex-based replacement without dateparser."""

    def _replace_ago(match: re.Match) -> str:
        amount = int(match.group(1))
        unit = match.group(2).lower()
        canonical = _RELATIVE_UNITS.get(unit)
        if canonical is None:
            return match.group(0)

        kwargs = {canonical: amount}
        result = ref - timedelta(**kwargs)
        return result.strftime("%Y-%m-%d")

    def _replace_simple(match: re.Match) -> str:
        word = match.group(0).lower()
        if word == "yesterday":
            return (ref - timedelta(days=1)).strftime("%Y-%m-%d")
        elif word == "today":
            return ref.strftime("%Y-%m-%d")
        elif word == "tomorrow":
            return (ref + timedelta(days=1)).strftime("%Y-%m-%d")
        return match.group(0)

    def _replace_last(match: re.Match) -> str:
        period = match.group(1).lower()
        if period == "night" or period == "week":
            return (ref - timedelta(weeks=1)).strftime("%Y-%m-%d")
        elif period == "month":
            return (ref - timedelta(days=30)).strftime("%Y-%m-%d")
        elif period == "year":
            return (ref - timedelta(days=365)).strftime("%Y-%m-%d")
        return match.group(0)

    text = _AGO_RE.sub(_replace_ago, text)
    text = _SIMPLE_RE.sub(_replace_simple, text)
    text = _LAST_RE.sub(_replace_last, text)
    return text
