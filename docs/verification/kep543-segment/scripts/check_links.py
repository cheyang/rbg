#!/usr/bin/env python3
"""Relative-link and TOC-anchor resolution check for the KEP-543 docs.

Usage: check_links.py <doc-dir containing README.md and README.zh-CN.md>
Polarity: contract. PASS when every relative target exists in the repo tree
and every in-document anchor resolves to a heading.
"""
import os
import re
import sys


def anchor_of(heading):
    h = heading.strip().lower()
    return re.sub(r"[^\w\s-]", "", h).replace(" ", "-")


def check(path, repo_root):
    text = open(path, encoding="utf-8").read()
    base = os.path.dirname(path)
    targets = re.findall(r"\]\(([^)#\s]+?)(#[^)]*)?\)", text)
    headings = {anchor_of(m.group(1)) for m in re.finditer(r"^#{1,6}\s+(.*)$", text, re.M)}
    # toc entries are links of the form (#anchor)
    toc = re.findall(r"\]\(#([^)]+)\)", text)
    problems = []
    for target, frag in targets:
        if target.startswith(("http", "mailto:")):
            continue
        p = os.path.normpath(os.path.join(base, target))
        if not os.path.isfile(p):
            problems.append("missing target: %s" % p)
    for a in toc:
        if a not in headings:
            problems.append("unresolved anchor: #%s" % a)
    return problems


def main():
    doc = sys.argv[1]
    repo_root = os.path.dirname(doc)
    ok = True
    for name in ("README.md", "README.zh-CN.md"):
        problems = check(os.path.join(doc, name), repo_root)
        if problems:
            ok = False
            for p in problems:
                print("FAIL %s: %s" % (name, p))
        else:
            print("PASS %s: all relative links and TOC anchors resolve" % name)
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
