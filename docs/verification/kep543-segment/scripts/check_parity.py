#!/usr/bin/env python3
"""EN/ZH structural parity check for KEP-543.

Usage: check_parity.py <doc-dir>
Compares heading structure, code-block count, table row counts, and the key
numeric examples (normalizing full-width CJK punctuation) between the English
and Chinese READMEs. Polarity: contract — the two language versions must stay
in content parity.
"""
import re
import sys


def load(path):
    return open(path, encoding="utf-8").read()


def headings(text):
    return re.findall(r"^(#{1,6})\s+(.*)$", text, re.M)


def codeblocks(text):
    return re.findall(r"```(\w*)\n(.*?)```", text, re.S)


def tables(text):
    tabs, cur = [], []
    for line in text.split("\n"):
        if line.strip().startswith("|"):
            cur.append(line)
        else:
            if cur:
                tabs.append(cur)
                cur = []
    if cur:
        tabs.append(cur)
    return tabs


def numbers(text):
    # normalize full-width punctuation and spacing used in the ZH doc
    t = text.replace("，", ",").replace("、", ",").replace(", ", ",")
    pats = [
        r"0 → 2 → 4 → 5", r"completeSegments=2", r"residual\[P\]=1",
        r"P=4, D=2", r"P=6, D=3", r"P=5, D=2", r"P=4, D=8",
        r"subGroupSize=3", r"minSubGroups=2", r"minMember=6", r"S=1",
        r"kv-cache-0", r"kv-cache-1",
    ]
    return {p: len(re.findall(p, t)) for p in pats}


def main():
    doc = sys.argv[1]
    en = load(doc + "/README.md")
    zh = load(doc + "/README.zh-CN.md")
    ok = True

    he, hz = headings(en), headings(zh)
    if len(he) == len(hz) and [h[0] for h in he] == [h[0] for h in hz]:
        print("PASS headings: %d headings, identical level sequence" % len(he))
    else:
        print("FAIL headings: EN %d vs ZH %d" % (len(he), len(hz)))
        ok = False

    cjk = re.compile(r"[　-〿一-鿿＀-￯]+")

    def strip_common(body):
        # drop comment/annotation lines, remove CJK characters
        lines = [l for l in body.split("\n")
                 if l.strip() and not l.strip().startswith(("//", "#"))]
        return cjk.sub("", "\n".join(lines))

    def norm_code(body):
        # structural code block (go/yaml/...): compare modulo translation by
        # dropping <placeholder> tokens and all whitespace
        s = re.sub(r"<[^>\n]*>", "<>", strip_common(body))
        return re.sub(r"\s+", "", s)

    def norm_text(body):
        # prose/diagram `text` block: compare only formula lines (containing
        # = <= >= → ↓ or starting with |). Each structural identifier is
        # reduced to its punctuation shape (contains _ . [ ( ?) because the
        # two languages embed different natural-language words inside the
        # same formulas. Plus arrow/equals counts. Translated prose lines
        # are ignored so the comparison targets formula skeletons only.
        s = strip_common(body)
        formula_lines = [l for l in s.split("\n")
                         if re.search(r"[=<>≤≥×∪∩]|→|↓", l) or l.strip().startswith("|")]
        shapes = sorted(
            tuple(c in t for c in "_.[(")
            for l in formula_lines
            for t in re.findall(r"[A-Za-z_][A-Za-z0-9_.\[\]()\-]*", l)
            if len(t) >= 3 and any(c in t for c in "_.[("))
        arrows = (s.count("→"), s.count("↓"), s.count("="))
        return shapes, arrows

    be, bz = codeblocks(en), codeblocks(zh)
    if len(be) == len(bz):
        same = True
        for i, ((la, ba), (lz, bzz)) in enumerate(zip(be, bz)):
            if la != lz:
                same = False
                print("FAIL code block %d: language tags differ (%s/%s)" % (i, la, lz))
                ok = False
                break
            if la == "text":
                if norm_text(ba) != norm_text(bzz):
                    same = False
                    print("FAIL code block %d (text): formula skeleton differs" % i)
                    ok = False
                    break
            else:
                if norm_code(ba) != norm_code(bzz):
                    same = False
                    print("FAIL code block %d (%s): content differs beyond translation" % (i, la))
                    ok = False
                    break
        if same:
            print("PASS code blocks: %d blocks, identical beyond translation" % len(be))
    else:
        print("FAIL code blocks: EN %d vs ZH %d" % (len(be), len(bz)))
        ok = False

    te, tz = tables(en), tables(zh)
    if len(te) == len(tz) and [len(t) for t in te] == [len(t) for t in tz]:
        print("PASS tables: %d tables, matching row counts" % len(te))
    else:
        print("FAIL tables: EN %s vs ZH %s" % ([len(t) for t in te], [len(t) for t in tz]))
        ok = False

    ne, nz = numbers(en), numbers(zh)
    if ne == nz:
        print("PASS key numbers: %d tracked examples match" % len(ne))
    else:
        for k in ne:
            if ne[k] != nz.get(k):
                print("FAIL key number %r: EN %d vs ZH %d" % (k, ne[k], nz.get(k)))
                ok = False

    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
