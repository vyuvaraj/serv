import re

def clean_text(text):
    # Strip spaces and resolve extra punctuation
    stripped = text.strip()
    clean = re.sub(r'[!#$?]+', '', stripped)
    return f"[Python formatter] Result: '{clean.upper()}'"
