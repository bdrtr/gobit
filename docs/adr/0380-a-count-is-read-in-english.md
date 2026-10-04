# ADR 0380 — A count is read in English

**Summary:** The count gate reads a number in digits and English words alone;
the language gate leaves no Turkish prose for it to read.

- **Status:** Accepted
- **Date:** 2026-10-04

## Context

ADR 0070 read a count "in both languages and every form", Turkish words and
their spaced compounds included, because two documents were Turkish by
decision. The Turkish ledger has been empty since 2026-10-03, and since D227
the language gate reads every text file git lists, so Turkish prose anywhere
fails the suite. The Turkish half of the vocabulary had nothing left to read,
and its data file, read for the first time by the widened scan, was itself
the only Turkish left in the tree (D228).

## Decision

The count gate reads a number in digits and in English words, hyphenated and
spaced compounds included, and nothing else. That no Turkish prose remains is
the language gate's to hold, not this gate's.

## Consequences

- A count written in Turkish without diacritics, which the language gate does
  not see, is read by no gate.
- The Turkish ten and its clash with the English preposition are gone, and so
  is the folding of Turkish letters before a word is looked up.
- The population nouns and the stop-words are English.
- The English units, tens and scale words are held whole by a test, not only
  by example, and a two-word compound is read as one number through the
  scanner itself.

## Rejected

- Keeping the Turkish half in case a Turkish document returns: the language
  gate refuses that document before this gate would read it.
- Exempting the vocabulary's data file from the language gate: it would keep a
  Turkish lookup table, in a tree whose ledger is empty, for a reader of
  nothing.
