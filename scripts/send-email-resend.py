#!/usr/bin/env python3
import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path

ENV_PATH = Path(os.environ.get("RQSTDEV_RESEND_ENV", "./resend.env"))
API_URL = "https://api.resend.com/emails"


def load_env(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    if not path.exists():
        return values
    for raw in path.read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip()
    return values


def build_message(email: str, code: str, purpose: str) -> tuple[str, str, str]:
    purpose_label = purpose.strip().lower() or "login"
    if purpose_label == "reset":
        subject = "rqstdev password reset code"
        heading = "Password reset code"
        intro = "Use this code to reset your rqstdev password."
    elif purpose_label == "signup":
        subject = "rqstdev signup verification code"
        heading = "Signup verification code"
        intro = "Use this code to complete your rqstdev signup."
    else:
        subject = "rqstdev login verification code"
        heading = "Login verification code"
        intro = "Use this code to finish signing in to rqstdev."

    text = f"{heading}\n\n{intro}\n\nCode: {code}\n\nIf you did not request this code, you can ignore this email."
    html = (
        '<div style="font-family:Arial,sans-serif;line-height:1.5;color:#111">'
        f"<h2>{heading}</h2>"
        f"<p>{intro}</p>"
        f'<p style="font-size:24px;font-weight:bold;letter-spacing:4px">{code}</p>'
        "<p>If you did not request this code, you can ignore this email.</p>"
        "</div>"
    )
    return subject, text, html


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--email", required=True)
    parser.add_argument("--code", required=True)
    parser.add_argument("--purpose", required=True)
    args = parser.parse_args()

    env = load_env(ENV_PATH)
    api_key = env.get("RESEND_API_KEY") or os.environ.get("RESEND_API_KEY", "").strip()
    from_email = env.get("RESEND_FROM_EMAIL") or os.environ.get("RESEND_FROM_EMAIL", "").strip()
    from_name = env.get("RESEND_FROM_NAME") or os.environ.get("RESEND_FROM_NAME", "rqstdev").strip()

    if not api_key:
        print("missing RESEND_API_KEY", file=sys.stderr)
        return 2
    if not from_email:
        print("missing RESEND_FROM_EMAIL", file=sys.stderr)
        return 2

    subject, text, html = build_message(args.email, args.code, args.purpose)
    payload = {
        "from": f"{from_name} <{from_email}>",
        "to": [args.email],
        "subject": subject,
        "text": text,
        "html": html,
    }

    request = urllib.request.Request(
        API_URL,
        data=json.dumps(payload).encode("utf-8"),
        headers={
            "Authorization": f"Bearer {api_key}",
            "Content-Type": "application/json",
            "User-Agent": "rqstdev-email-script/1.0",
        },
        method="POST",
    )

    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            body = response.read().decode("utf-8", "replace")
            if response.status < 200 or response.status >= 300:
                print(body, file=sys.stderr)
                return 1
            print(body)
            return 0
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        print(f"HTTP {exc.code}: {body}", file=sys.stderr)
        return 1
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
