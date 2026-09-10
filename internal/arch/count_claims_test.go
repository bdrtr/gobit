package arch_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file enforces ONE invariant: WHEN THE PROSE STATES HOW MANY OF SOMETHING
// THIS TREE HOLDS, THE TREE HOLDS THAT MANY.
//
// It is the COUNT half of docs/gaps.md's D29. The route-address gate next door
// settles one class of claim; the sweep that opened D29 named three more, and the
// count is the one that bites. On 2026-09-08, with every suite green, FIVE count
// claims in this tree were found false by hand: an index saying "thirty-four
// records" when there were 51, a ledger line saying "260 files" when it was 200, a
// gate godoc pricing the admin panel as "most" of 52 undescribed routes when it
// binds 18, an ADR calling two categories "the same size" when its own record
// measured 26 and 27, and a measurement pricing a change that was not the one made.
// None of them is reachable by a name resolver: every reference resolved, and the
// sentence was still wrong.
//
// # Why a VOCABULARY and not "every number"
//
// "Every number in every document" is not a population, it is noise, and its
// exemption list would have to carry its own false positives — the shape
// docs/gaps.md D16 records. Measured on the tree this was written against: the bare
// shape "a number followed by a plural noun" occurs 185 times for "modules" alone
// and 129 times for "tables", and almost every one of them is a SUBSET rather than
// a total — "two modules share a schema", "the two tables this store owns", "two
// plugins each believing they own the reporting". No article rule separates them:
// "the four core packages" is a subset and "the ten plugins" is a total.
//
// So the population is a CLOSED VOCABULARY: [countedPopulations]. Each entry is one
// countable thing whose size the tree computes for itself, from a source that is not
// the sentence — the module directories, the packages under core/, the ADR record
// files, the bullets of a document. A sentence enters the audit only when it names
// that population BY ITS PATH on the same line, which is the shape a total is
// written in and a subset is not.
//
// # What this audit does NOT guarantee
//
//   - It does not read a number the vocabulary has no entry for. A sentence
//     pricing tables, routes, endpoints, columns or gates is outside it, and the
//     measurement above says why: for those nouns the tree's prose is dominated by
//     subsets and the audit would be an exemption list with a check attached.
//     Two live examples were found while writing this and repaired by hand rather
//     than by widening: the route gate's own godoc priced the described surface at
//     276 of 328 when the tree binds 330 and describes 278.
//   - It does not check a number that is not a COUNT. A version, a port, a byte
//     size, a timeout and an ADR number are all numbers next to nouns.
//   - It reads a LINE. A claim whose number and noun fall on different lines is
//     invisible, and so is one whose number sits in the sentence before.
//   - It does not say the sentence AROUND the number is true. "seventeen isolated
//     commerce modules" resolves on the count alone; whether they are isolated is
//     [TestModulesDoNotImportEachOther]'s question.
//   - THE DATED RECORDS ARE OUT OF SCOPE — every ADR record and CHANGELOG.md (see
//     [countDatedRecord]). The section below is the rule and its price.
//
// # The dated records are out of scope, and the boundary is WIDER here
//
// A dated record states what was true ON ITS DATE. Holding it to today's tree makes
// a defect out of every historical sentence, and CLAUDE.md leaves no repair for one:
// the records are "the historical record", amended "by adding, not by editing".
// That is the same rule [routeDatedRecord] applies, and this gate reuses the
// reasoning — but not the boundary. The route gate freezes the records only up to
// [routeFrozenADR] and audits every record written after it. This gate excludes ALL
// of them, at any number, and the reason is measured rather than assumed:
//
//   - a route address in a new record is expected to still be true tomorrow, so
//     auditing it costs the writer nothing. A COUNT in a new record is stale the
//     day the tree grows, which is often the next commit;
//   - and there is no second reason. An earlier draft of this godoc offered one
//     — that ADR 0069's "`core/` goes from sixteen packages to seventeen" is a
//     transition whose halves fall on different lines, which a line-anchored
//     scanner would read as a claim about today. That was checked and it is
//     FALSE: with [countDatedRecord] switched off, the records yield no claim at
//     all, ADR 0069's sentence included, because `core` anchors only in a
//     README's layout column. The argument was for a misreading that does not
//     happen, and one true reason is worth more than two of which one is wrong.
//
// THE PRICE, measured with the exclusion switched off rather than reasoned about:
// ONE claim, and it is not in a record. CHANGELOG.md prices the reports under
// `docs/measurements/` at fifteen, in the entry announcing that directory, against
// a tree that now holds thirty-two. That sentence says what was moved out of
// gaps.md THAT DAY — it is historical, which is the argument FOR excluding the
// changelog rather than a cost of doing so. The ADR records cost zero.
// [TestTheCountClaimScannerIsNotBlind] holds the remainder by NAME, so the
// exclusion cannot grow silently.
//
// A ROW OF docs/adr/README.md is excluded for the same reason one level out. The
// index is not a record and stays in scope as a FILE — the "thirty-four records"
// defect above lived in exactly such an index — but each of its table rows QUOTES
// the title and decision sentence of the record it links to, verbatim. A count
// inside a quotation is the quoted record's, not the index's, and the index's own
// prose stays audited.

// countedPopulation is one countable thing in the vocabulary: something the prose
// states a size for and the tree can size for itself.
type countedPopulation struct {
	// name is what a failure calls it.
	name string
	// anchor is the population's own path, and it is what admits a sentence into
	// the audit (see [countAnchored]). It is not where the size comes from.
	anchor string
	// nouns matches a token naming ONE MEMBER of the population, in either
	// language. The tree's prose is Turkish in two files by decision (ADR 0012's
	// ledger), and a count is written in words there as often as here.
	nouns *regexp.Regexp
	// floor is the smallest size [countedPopulation.size] may return before the
	// computation is treated as blind rather than as an answer. A population that
	// has emptied out reports every claim about it as false; a population whose
	// directory MOVED reports zero and would do the same.
	floor int
	// size computes the population from the tree.
	size func(t *testing.T) int
}

// countedPopulations is the vocabulary: the countable things this audit knows how
// to price.
//
// The entries were chosen by measurement and not by taste. Each one is (a) stated
// somewhere in the prose as a total, or is the kind of total the layout block and
// the reading table are written in, and (b) computable from a source that is not
// the sentence. Nouns the tree writes mostly as subsets — tables, routes, columns,
// endpoints, gates — are deliberately absent; see the head of this file.
var countedPopulations = []countedPopulation{
	{
		name:   "the commerce modules under internal/modules",
		anchor: modulesDir,
		nouns:  regexp.MustCompile(`^(modules|modul|modulu|moduller|modulunu)$`),
		floor:  10,
		size:   func(t *testing.T) int { return len(moduleNames(t)) },
	},
	{
		name:   "the published packages under core/",
		anchor: "core",
		nouns:  regexp.MustCompile(`^(packages|paket|paketi|paketler|paketleri)$`),
		floor:  10,
		size:   func(t *testing.T) int { return len(countGoPackageDirs(t, "core")) },
	},
	{
		name:   "the in-tree plugins",
		anchor: "plugins",
		nouns:  regexp.MustCompile(`^(plugins|eklenti|eklentisi|eklentiler|eklentileri|eklentinin)$`),
		floor:  5,
		size:   func(t *testing.T) int { return len(countSubdirectories(t, "plugins")) },
	},
	{
		name:   "the workflow packages under internal/workflows",
		anchor: workflowsDirName,
		nouns:  regexp.MustCompile(`^(workflows|saga|sagalar|saga'lar)$`),
		floor:  3,
		size:   func(t *testing.T) int { return len(countSubdirectories(t, workflowsDirName)) },
	},
	{
		name:   "the decision records under docs/adr",
		anchor: "docs/adr",
		nouns:  regexp.MustCompile(`^(records|record|kayit|kayitlar|adr|adrs)$`),
		floor:  20,
		size:   countADRRecords,
	},
	{
		name:   "the reports under docs/measurements",
		anchor: "docs/measurements",
		nouns:  regexp.MustCompile(`^(measurements|reports|olcum|olcumler|rapor|raporlar)$`),
		floor:  10,
		size:   countMeasurementReports,
	},
	{
		name:   "the entries of docs/known-limits.md",
		anchor: knownLimitsDoc,
		nouns:  regexp.MustCompile(`^(items|entries|madde|maddeler)$`),
		floor:  10,
		size:   func(t *testing.T) int { return countMarkdownLines(t, knownLimitsDoc, "- ") },
	},
	{
		name:   "the groups of docs/known-limits.md",
		anchor: knownLimitsDoc,
		nouns:  regexp.MustCompile(`^(groups|kume|kumeler|grup|gruplar)$`),
		floor:  2,
		size:   func(t *testing.T) int { return countMarkdownLines(t, knownLimitsDoc, "## ") },
	},
	{
		name:   "the topics plugins/webhookout forwards",
		anchor: forwardedTopicsAnchor,
		nouns:  regexp.MustCompile(`^(topics|topic|konu|konular|konuyu|konusu)$`),
		floor:  3,
		size:   countForwardedTopics,
	},
}

// forwardedTopicsAnchor is the plugin whose forwarded-topic list is priced.
const forwardedTopicsAnchor = "plugins/webhookout"

// countForwardedTopics counts the entries of the plugin's ForwardedTopics slice.
//
// # Why this population, when tables and routes were refused
//
// It passes both tests the head of this file sets. It is stated as a TOTAL —
// the slice is documented as the whole set of topics this repository publishes,
// and the plugin's own gate fails in both directions on it — and it is
// computable from a source that is not the sentence, which is the slice
// literal itself.
//
// It was added the day the class bit. ADR 0121 took the list from four topics
// to six, and FOUR sentences across the plugin's two files went on saying four:
// its package prose, its subscription comment, the slice's own godoc, and a
// refusal that priced outbox coverage at "one topic in four". Every gate in the
// tree was green over all of them, because "topics" was not in this vocabulary.
//
// The size is read from the AST rather than by grepping the constants: a
// commented-out entry, or a constant declared and never listed, would both fool
// a line count, and it is exactly the drift between the list and its prose that
// this entry exists to catch.
func countForwardedTopics(t *testing.T) int {
	t.Helper()

	fset := token.NewFileSet()
	path := filepath.Join(repoRoot, forwardedTopicsAnchor, "module.go")
	tree, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err, "%s could not be parsed", path)

	total := -1
	for _, decl := range tree.Decls {
		general, ok := decl.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "ForwardedTopics" {
				continue
			}
			require.Len(t, value.Values, 1, "ForwardedTopics must have one initialiser")
			literal, ok := value.Values[0].(*ast.CompositeLit)
			require.True(t, ok, "ForwardedTopics must be a slice literal, got %T", value.Values[0])
			total = len(literal.Elts)
		}
	}

	require.NotEqual(t, -1, total, "ForwardedTopics was not found in %s", path)

	return total
}

// knownLimitsDoc is the document whose entries and groups the reading tables of
// both READMEs price.
const knownLimitsDoc = "docs/known-limits.md"

// countSubdirectories returns the immediate subdirectories of a tree, which is what
// "a plugin" and "a workflow" mean in this repository: one directory each.
func countSubdirectories(t *testing.T, tree string) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(repoRoot, tree))
	require.NoError(t, err, "%s could not be read", tree)

	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}

	return dirs
}

// countGoPackageDirs returns every directory under a tree that holds production Go
// source, which is what "a package" means when the prose prices core/.
//
// It counts DIRECTORIES rather than parsing package clauses because that is the
// unit the sentence uses: core/eventbus/outbox is a package a reader can import and
// the README lists it as one.
func countGoPackageDirs(t *testing.T, tree string) []string {
	t.Helper()

	seen := map[string]bool{}
	root := filepath.Join(repoRoot, tree)
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(current, ".go") || strings.HasSuffix(current, "_test.go") {
			return nil
		}
		seen[filepath.Dir(current)] = true

		return nil
	})
	require.NoError(t, err, "%s could not be walked", tree)

	dirs := make([]string, 0, len(seen))
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	return dirs
}

// countADRRecords counts the decision records, which are the numbered files and not
// the index beside them.
func countADRRecords(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(repoRoot, "docs", "adr"))
	require.NoError(t, err, "docs/adr could not be read")

	numbered := regexp.MustCompile(`^\d{4}-.*\.md$`)
	total := 0
	for _, entry := range entries {
		if !entry.IsDir() && numbered.MatchString(entry.Name()) {
			total++
		}
	}

	return total
}

// countMeasurementReports counts the reports, which is every document under
// docs/measurements except the index that lists them.
func countMeasurementReports(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(repoRoot, "docs", "measurements"))
	require.NoError(t, err, "docs/measurements could not be read")

	total := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") && entry.Name() != "README.md" {
			total++
		}
	}

	return total
}

// countMarkdownLines counts the lines of a document that open with a prefix, which
// is how a markdown document says "an entry" (a top-level bullet) and "a group" (a
// second-level heading).
func countMarkdownLines(t *testing.T, doc, prefix string) int {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot, doc))
	require.NoError(t, err, "%s could not be read", doc)

	total := 0
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, prefix) {
			total++
		}
	}

	return total
}

// countWord splits a line into tokens, keeping paths WHOLE.
//
// The "/" and the "." stay INSIDE a token on purpose: it is what makes
// `internal/modules/order/module.go` one word rather than the three words
// "internal", "modules" and "order". Splitting it turns a path into a noun and a
// version into a number — measured, that alone produced seven false claims, among
// them "the b2b module" read as the number two and "PostgreSQL 16.14
// (docs/measurements/…)" read as fourteen measurements.
var countWord = regexp.MustCompile(`[\p{L}\p{N}_][\p{L}\p{N}_'’./-]*`)

// countASCIIFold flattens the six Turkish letters outside ASCII onto their ASCII
// shapes, so every table below can be written in plain ASCII.
//
// The pairs are written as \u ESCAPES rather than as letters. That is this
// repository's stated preference wherever the letters are DATA rather than prose —
// the same solution the product module's fold map and the case-folding probe took —
// and it keeps this file out of the language ledger without an exemption entry.
// The code points, in order: DOTLESS SMALL I, CAPITAL I WITH DOT ABOVE, SMALL and
// CAPITAL G WITH BREVE, SMALL and CAPITAL C WITH CEDILLA, SMALL and CAPITAL S WITH
// CEDILLA, SMALL and CAPITAL O WITH DIAERESIS, SMALL and CAPITAL U WITH DIAERESIS.
//
// Folding also buys the diacriticless spelling for free, which matters: the
// language detector is knowingly blind to Turkish written without diacritics, and a
// count claim spelled that way is the same claim.
var countASCIIFold = strings.NewReplacer(
	"\u0131", "i", "\u0130", "i",
	"\u011F", "g", "\u011E", "g",
	"\u00E7", "c", "\u00C7", "c",
	"\u015F", "s", "\u015E", "s",
	"\u00F6", "o", "\u00D6", "o",
	"\u00FC", "u", "\u00DC", "u",
)

// countFold puts a token into the shape the tables below are written in.
//
// The fold runs BEFORE the lowering on purpose: Go lowers the Turkish capital I
// with a dot into two runes, and the table would never match it again.
func countFold(word string) string {
	return strings.ToLower(countASCIIFold.Replace(word))
}

// countEnglishUnits are the English words for one through nineteen.
var countEnglishUnits = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	"eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
	"sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19,
}

// countEnglishTens are the English words for the round tens.
var countEnglishTens = map[string]int{
	"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50,
	"sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90,
}

// countNumeralsFile holds the Turkish half of the vocabulary.
//
// It is a file rather than a table in this source for one reason, and the reason is
// the language rule: one of the Turkish number words is on the detector's own
// safe-word list, so writing it here would put a new line in the language ledger
// for what is a lookup table, and the ledger may only shrink. An escape does not
// help — the detector unquotes a literal before reading it, which is correct of it.
const countNumeralsFile = "testdata/turkish-numerals.txt"

// countNumerals is the number vocabulary one scan reads with.
type countNumerals struct {
	// units are the words for one through nineteen, in both languages.
	units map[string]int
	// tens are the words for the round tens from twenty up, in both languages.
	tens map[string]int
	// turkishTen is the word for ten in Turkish, and it is the one word in the
	// vocabulary that is never a number on its own: it is also the English
	// preposition, and this repository's prose carries "on both tables" and "ON
	// ALL TABLES IN SCHEMA public". [countNumerals.at] reads it ONLY as the head
	// of a compound, where the next token is a Turkish unit. The ambiguity is
	// paid on the side that stays SILENT rather than the side that invents a
	// claim: a Turkish sentence pricing a population at exactly ten goes unread.
	turkishTen string
	// scales are the words that multiply the number in front of them —
	// "hundred", "thousand" and their Turkish counterparts.
	//
	// They are a HARD STOP rather than a multiplier, and a claim that reaches
	// one is read as no claim at all. Without them "one hundred records" reads
	// as 1 and "one hundred and five records" reads as 5, which is the reader
	// INVENTING a claim — the failure this vocabulary pays for in silence
	// everywhere else.
	//
	// The premise this used to give — "the reader is built for counts of a
	// population this repository holds tens of" — EXPIRED on 2026-09-09, when
	// the decision records passed a hundred. What follows from that is a rule
	// about the PROSE rather than machinery here: a count below a hundred is
	// spelled out, and one at or above it is written as a DIGIT, which this
	// reader has always read. The scale words stay a hard stop so a sentence
	// that spells one out is reported as unpriced instead of being misread.
	scales map[string]bool
	// turkishScales are the scale words read out of [countNumeralsFile], kept in
	// order so a test can name one WITHOUT spelling it: a Turkish word written
	// into this file would put a line in the language ledger for what is a
	// lookup table, and the ledger may only shrink (ADR 0012).
	turkishScales []string
}

// countEnglishScales are the English scale words. Their Turkish counterparts are
// in [countNumeralsFile] with a value of 0, because one of them is an ordinary
// English word this repository writes in its own source.
var countEnglishScales = map[string]bool{
	"hundred": true, "hundreds": true,
	"thousand": true, "thousands": true,
	"million": true, "millions": true,
}

// countNumeralWords builds the vocabulary, English inline and Turkish from
// [countNumeralsFile].
//
// A short read is refused. A vocabulary that has lost its Turkish half reports the
// two Turkish documents as carrying no claim at all, which is the same clean tree
// nobody walked that this whole audit exists to refuse.
func countNumeralWords(t *testing.T) countNumerals {
	t.Helper()

	vocabulary := countNumerals{
		units:  map[string]int{},
		tens:   map[string]int{},
		scales: map[string]bool{},
	}
	for word := range countEnglishScales {
		vocabulary.scales[word] = true
	}
	for word, value := range countEnglishUnits {
		vocabulary.units[word] = value
	}
	for word, value := range countEnglishTens {
		vocabulary.tens[word] = value
	}

	body, err := os.ReadFile(countNumeralsFile)
	require.NoError(t, err, "%s could not be read", countNumeralsFile)

	units, tens, scales := 0, 0, 0
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		head, word, found := strings.Cut(trimmed, " ")
		require.True(t, found, "%s: %q is not a value and a word", countNumeralsFile, line)
		value, err := strconv.Atoi(head)
		require.NoError(t, err, "%s: %q does not open with a value", countNumeralsFile, line)
		word = strings.TrimSpace(word)

		switch {
		case value == 0:
			vocabulary.scales[word] = true
			vocabulary.turkishScales = append(vocabulary.turkishScales, word)
			scales++
		case value == 10:
			vocabulary.turkishTen = word
		case value < 10:
			vocabulary.units[word] = value
			units++
		default:
			vocabulary.tens[word] = value
			tens++
		}
	}

	require.GreaterOrEqual(t, units, 9,
		"%s lost its units; the Turkish documents would report no claim at all",
		countNumeralsFile)
	require.GreaterOrEqual(t, tens, 8,
		"%s lost its tens; a compound over twenty would go unread", countNumeralsFile)
	require.NotEmpty(t, vocabulary.turkishTen,
		"%s names no ten, so no teen compound can be read", countNumeralsFile)
	require.GreaterOrEqual(t, scales, 2,
		"%s lost its scale words, so a Turkish sentence above ninety-nine would be "+
			"read as the number in front of the scale rather than left alone",
		countNumeralsFile)

	return vocabulary
}

// countDigits matches a token that is a plain decimal number.
//
// The leading zero is refused, and that single rule is what keeps "ADR 0026",
// "migration 000003" and "0051-an-unidentified-write" out of the audit: a record
// number is not a count, and this tree writes far more of them than it writes
// counts.
var countDigits = regexp.MustCompile(`^[1-9]\d{0,5}$`)

// countFunctionWords are the words that may NOT stand between a number and the noun
// it counts.
//
// A gap is allowed at all because a total is written with its adjectives —
// "seventeen isolated commerce modules" — and it is bounded at two words for the
// same reason. Without the list the gap bridges a preposition and welds two
// unrelated phrases together; the two lines that forced it are in
// [TestTheCountClaimShapeBitesAndStops], where one prices a surface count and the
// other opens a numbered list, and a wider gap read each of them as a claim about
// the module tree. The list is closed function words rather than a taste list, and
// it carries no false positive of its own, because nothing in it is ever an
// adjective.
var countFunctionWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "in": true, "on": true,
	"at": true, "to": true, "and": true, "or": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "that": true, "this": true, "these": true,
	"those": true, "its": true, "their": true, "it": true, "as": true, "by": true,
	"for": true, "from": true, "with": true, "into": true, "over": true, "under": true,
	"than": true, "then": true, "not": true, "no": true, "only": true, "just": true,
	"each": true, "every": true, "any": true, "some": true, "all": true, "both": true,
	"which": true, "who": true, "what": true, "when": true, "where": true, "there": true,
	"ve": true, "ile": true, "bu": true, "su": true, "gibi": true,
	"ise": true, "ki": true, "da": true, "de": true,
}

// at reads the number the token at an index carries, if it carries one, together
// with how many tokens that number consumed.
//
// The span is two for a Turkish compound — a ten followed by a unit, written as two
// words — and one for everything else, since an English compound is hyphenated and
// already arrives as a single token. Consuming both tokens is what stops the unit
// half from being read a second time as a claim of its own: "fifty-one records"
// written the Turkish way would otherwise also say "one record", which is a
// sentence nobody wrote.
func (n countNumerals) at(tokens []string, index int) (value, span int) {
	word := countFold(tokens[index])

	// A scale word yields nothing, and so does any number standing in front of
	// one. "one hundred records" is a claim this reader cannot compose, and the
	// vocabulary's rule for what it cannot compose is silence rather than a
	// wrong number — the same rule the Turkish ten is read under.
	if n.scales[word] {
		return -1, 0
	}
	if index+1 < len(tokens) && n.scales[countFold(tokens[index+1])] {
		return -1, 0
	}
	if index+2 < len(tokens) && n.scales[countFold(tokens[index+2])] {
		// The two-token forms: "twenty five hundred", and the English "one
		// hundred and five", whose "and" this reader would otherwise walk past
		// to price the population at five.
		if _, ok := n.units[countFold(tokens[index+1])]; ok {
			return -1, 0
		}
	}

	if countDigits.MatchString(word) {
		parsed, err := strconv.Atoi(word)
		if err != nil {
			return -1, 0
		}

		return parsed, 1
	}

	unit := -1
	if index+1 < len(tokens) {
		if next, ok := n.units[countFold(tokens[index+1])]; ok && next < 10 {
			unit = next
		}
	}

	if tens, ok := n.tens[word]; ok {
		if unit > 0 {
			return tens + unit, 2
		}

		return tens, 1
	}
	// The Turkish ten heads a compound and is read nowhere else; see [countNumerals].
	if word == n.turkishTen {
		if unit > 0 {
			return 10 + unit, 2
		}

		return -1, 0
	}
	if head, ok := n.units[word]; ok {
		return head, 1
	}
	// An English compound is hyphenated and arrives as one token: "fifty-one".
	if head, tail, found := strings.Cut(word, "-"); found {
		tens, tensOK := n.tens[head]
		last, lastOK := n.units[tail]
		if tensOK && lastOK && last < 10 {
			return tens + last, 1
		}
	}

	return -1, 0
}

// countUnquoted blanks the spans inside double quotes.
//
// A number inside quotation marks is being REPORTED, not asserted: the prose is
// quoting a sentence somebody else wrote, usually one this repository has since
// corrected. Two godocs in this package quote ADR 0069's "`core/` goes from
// sixteen packages to seventeen" while explaining why an argument about it was
// wrong, and holding them to today's tree would demand that a quotation be
// falsified to stay green.
//
// It is a cheap rule and the scanner can afford it because it reads PROSE —
// comments and markdown — where a double quote is a quotation. It does not read
// Go string literals, where the same mark means something else.
//
// The cost is stated: a total that somebody writes inside quotation marks is not
// audited. Nothing in the tree does that today, and a writer who wants the
// sentence held can simply not quote it.
func countUnquoted(line string) string {
	var (
		out   strings.Builder
		quote bool
	)

	for _, r := range line {
		if r == '"' || r == '\u201C' || r == '\u201D' {
			quote = !quote
			out.WriteRune(' ')

			continue
		}
		if quote {
			out.WriteRune(' ')

			continue
		}

		out.WriteRune(r)
	}

	return out.String()
}

// countAnchored reports whether a line names a population BY ITS PATH, which is
// what admits the line into the audit.
//
// A MULTI-SEGMENT anchor — internal/modules, docs/adr, docs/known-limits.md — is
// matched as a token that is the path or sits under it. A SINGLE-SEGMENT anchor —
// core, plugins — cannot be, because "core" and "plugins" are also ordinary words:
// "the four core packages" and "two plugins each believing they own the reporting"
// would both anchor, and both are subsets. A single segment therefore anchors only
// in the DIRECTORY-LAYOUT column, where it opens the line and a run of spaces
// separates it from the description — the shape both READMEs draw their tree in,
// and a shape prose does not fall into by accident.
func countAnchored(line, anchor string) bool {
	if !strings.Contains(anchor, "/") {
		// A single segment anchors in PROSE when it is written as a path with a
		// trailing slash — "the eleven plugins under plugins/" — and only then.
		// The exact token is required: "core/" anchors, "core" does not (it is
		// an ordinary word, and "the four core packages" is a subset), and
		// "core/query" does not either (it names something else under it).
		//
		// This is the opt-in half of the anchor rule. A writer who means the
		// whole population says so by naming its path, and a sentence that does
		// not is left alone rather than guessed at.
		for _, token := range countWord.FindAllString(line, -1) {
			if strings.Trim(token, ".") == anchor+"/" {
				return true
			}
		}

		// The other half is the README's directory-layout column, where the
		// segment opens the line and a run of spaces separates it from the
		// description. The line is trimmed and a trailing slash is allowed,
		// because "core/" and an indented column are how that layout is
		// ordinarily written — and a claim that disappears when somebody adds a
		// slash or re-aligns a column is a gate that goes QUIET on an edit
		// nobody would think twice about. Measured on this gate: with the strict
		// form, writing the directory as "core/" removed the claim and the whole
		// package stayed green with a false count standing.
		rest, opens := strings.CutPrefix(strings.TrimSpace(line), anchor)
		if !opens {
			return false
		}

		rest, _ = strings.CutPrefix(rest, "/")

		return strings.HasPrefix(rest, " ") &&
			strings.HasPrefix(strings.TrimLeft(rest, " "), "#")
	}

	for _, token := range countWord.FindAllString(line, -1) {
		trimmed := strings.Trim(token, "./")
		if trimmed == anchor || strings.HasPrefix(trimmed, anchor+"/") {
			return true
		}
	}

	return false
}

// countClaim is one sentence pricing one population.
type countClaim struct {
	population string
	file       string
	line       int
	// stated is the number the prose wrote.
	stated int
	// phrase is the number and the noun as written, so a failure can be found by
	// searching for it rather than by counting to a column.
	phrase string
	// markdown says the claim came from a document rather than a Go comment.
	markdown bool
}

// countADRIndexRow matches a row of docs/adr/README.md, which is a link to a record
// followed by that record's own title and decision sentence.
var countADRIndexRow = regexp.MustCompile(`\(\d{4}-[^)]*\.md\)`)

// countDatedRecord says whether a document is a DATED RECORD, which this audit does
// not read.
//
// Two file classes are records: CHANGELOG.md, and EVERY ADR record at any number.
// The rule, why the boundary is wider than [routeDatedRecord]'s, and what it costs
// are at the head of this file, measured.
func countDatedRecord(doc string) bool {
	if doc == "CHANGELOG.md" || strings.HasSuffix(doc, "/CHANGELOG.md") {
		return true
	}

	return routeADRRecord.MatchString(doc)
}

// countQuotedRow says whether a line is a row of the ADR index quoting the record it
// links to. The index itself stays in scope; see the head of this file.
func countQuotedRow(doc, line string) bool {
	if doc != "docs/adr/README.md" && !strings.HasSuffix(doc, "/docs/adr/README.md") {
		return false
	}

	return countADRIndexRow.MatchString(line)
}

// countClaimsIn reads the claims one line makes about one population.
//
// The shape is a NUMBER, then at most two words that are not function words and not
// numbers themselves, then a token naming a member of the population. The first
// noun ends the claim: "twenty-one items in four groups" is two claims and not one
// spanning both.
func countClaimsIn(line string, population countedPopulation, numerals countNumerals) []countClaim {
	line = countUnquoted(line)

	if !countAnchored(line, population.anchor) {
		return nil
	}

	tokens := countWord.FindAllString(line, -1)

	var claims []countClaim
	for at := 0; at < len(tokens); at++ {
		stated, span := numerals.at(tokens, at)
		if stated < 0 {
			continue
		}
		for cursor := at + span; cursor < len(tokens) && cursor < at+span+3; cursor++ {
			word := countFold(tokens[cursor])
			if value, _ := numerals.at(tokens, cursor); value >= 0 {
				break
			}
			if population.nouns.MatchString(word) {
				claims = append(claims, countClaim{
					population: population.name,
					stated:     stated,
					phrase:     strings.Join(tokens[at:cursor+1], " "),
				})

				break
			}
			if countFunctionWords[word] {
				break
			}
		}
		at += span - 1
	}

	return claims
}

// collectCountClaims reads every count claim the documents and the Go comments make
// about the vocabulary.
func collectCountClaims(t *testing.T) []countClaim {
	t.Helper()

	numerals := countNumeralWords(t)
	claims := collectMarkdownCountClaims(t, numerals)

	return append(claims, collectCommentCountClaims(t, numerals)...)
}

// collectMarkdownCountClaims reads the claims the documents make.
func collectMarkdownCountClaims(t *testing.T, numerals countNumerals) []countClaim {
	t.Helper()

	var claims []countClaim
	for _, doc := range markdownDocs(t) {
		if countDatedRecord(doc.path) {
			continue
		}

		prose := &routeProse{}
		for index, raw := range doc.lines {
			line := prose.live(raw)
			if countQuotedRow(doc.path, line) {
				continue
			}
			for _, population := range countedPopulations {
				for _, claim := range countClaimsIn(line, population, numerals) {
					claim.file, claim.line, claim.markdown = doc.path, index+1, true
					claims = append(claims, claim)
				}
			}
		}

		assert.False(t, prose.struck,
			"%s ends inside a struck span, so everything after the opening mark was "+
				"blanked and this audit read a document that stops early", doc.path)
	}

	return claims
}

// collectCommentCountClaims reads the claims the Go comments make.
//
// Test files are included for the reason [collectCommentRouteClaims] includes them:
// the densest prose in this repository is in the gate godocs, and a gate godoc that
// prices its own population wrongly is the exact defect this class records — two
// were found in one while this was written.
func collectCommentCountClaims(t *testing.T, numerals countNumerals) []countClaim {
	t.Helper()

	fset := token.NewFileSet()

	var claims []countClaim
	err := filepath.WalkDir(repoRoot, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// skippedDirs rather than a pair of names: it holds .claude, where
			// the agent tooling keeps git WORKTREES — full copies of this
			// repository. Walking one makes every claim in the tree arrive
			// twice and lets a copy that belongs to no commit inject or
			// withhold one.
			if slices.Contains(skippedDirs, entry.Name()) {
				return filepath.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(current, ".go") {
			return nil
		}
		tree, err := parser.ParseFile(fset, current, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("%s: %w", current, err)
		}
		relative, err := filepath.Rel(repoRoot, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)

		for _, group := range tree.Comments {
			prose := &routeProse{}
			for _, comment := range group.List {
				at := fset.Position(comment.Pos()).Line
				for offset, raw := range strings.Split(comment.Text, "\n") {
					line := prose.live(raw)
					for _, population := range countedPopulations {
						for _, claim := range countClaimsIn(line, population, numerals) {
							claim.file, claim.line = relative, at+offset
							claims = append(claims, claim)
						}
					}
				}
			}

			assert.False(t, prose.struck,
				"%s: a comment group ends inside a struck span, so the rest of it was "+
					"blanked and never audited", relative)
		}

		return nil
	})
	require.NoError(t, err, "the Go comments could not be scanned")

	return claims
}

// countSizes prices every population in the vocabulary and refuses a size that has
// fallen through its floor.
//
// A population that reads zero is the failure mode this whole audit has to survive:
// it would report every sentence about that population as false, in the same breath
// as it reported a tree it never walked.
func countSizes(t *testing.T) map[string]int {
	t.Helper()

	sizes := map[string]int{}
	for _, population := range countedPopulations {
		size := population.size(t)
		require.GreaterOrEqual(t, size, population.floor,
			"%s priced at %d, which is under the floor of %d.\n"+
				"Either the tree really shrank that far — and the floor moves with it, on "+
				"purpose, in a diff somebody reads — or the computation has gone BLIND and "+
				"this audit is about to call every sentence about it false.",
			population.name, size, population.floor)
		sizes[population.name] = size
	}

	return sizes
}

// TestTheCountsInTheProseAreTrue verifies that a number the prose states for a
// population in [countedPopulations] is the number the tree computes.
//
// # What a failure means
//
// The sentence is wrong about today's tree, and there are three honest endings:
//
//  1. the tree grew or shrank and the sentence should follow it — which is the
//     answer nearly every time, because the tree moves and the sentence does not;
//  2. the sentence recorded a fact that has since changed and the document is a
//     living one, in which case the repository's marker is to strike the old
//     statement through and write the correction beside it, exactly as
//     docs/measurements/b2-remainder.md does with its own findings. A struck span
//     leaves this audit (see [routeProse]);
//  3. the sentence is not about this population at all and the noun collided —
//     which is a finding about the VOCABULARY, and the repair is in this file:
//     narrow the noun, not the sentence.
//
// There is no exemption list, and that is not an oversight. An exemption here would
// have to say "this document may state a number the tree contradicts", and no
// document in scope may.
func TestTheCountsInTheProseAreTrue(t *testing.T) {
	t.Parallel()

	sizes := countSizes(t)
	claims := collectCountClaims(t)

	for _, claim := range claims {
		actual := sizes[claim.population]
		assert.Equal(t, actual, claim.stated,
			"%s:%d says %q — %d — and the tree holds %d.\n"+
				"The population is %s, counted from the tree itself. Correct the sentence, "+
				"or strike it through if the document is recording what was once true.",
			claim.file, claim.line, claim.phrase, claim.stated, actual, claim.population)
	}
}

// countClaimedToday names the population each README is known to price.
//
// It is a LIST OF NAMES rather than a count of claims, and that is the whole
// repair. The control shipped as "at least eight claims, at least four
// populations, from at least two files", and those thresholds were measured
// against ten claims over five populations — so a claim could go silent and the
// floors would still hold. It was demonstrated rather than argued: writing the
// layout column's directory as "core/" instead of "core" removed BOTH core
// claims, and the arch package stayed green with two false counts standing in
// the two files where all six of this gate's opening defects lived.
//
// A name cannot go quiet the same way. A population that legitimately stops
// being claimed has to leave this list, which puts the decision in a diff
// somebody reads — the doctrine the size side already uses with its floors.
//
// The two READMEs are the same list because they are translations of one
// another: a count in one and not the other is itself a defect, and this gate
// found exactly that shape on the day it was written, where the two files
// disagreed with each other about the number of limits entries.
//
// Three of the eight populations in the vocabulary — the plugins, the workflow
// packages and the measurement reports — are priced NOWHERE in the tree, so
// they are absent here. They are kept in the vocabulary because the sentence
// that prices them is the one this gate exists to catch on the day somebody
// writes it.
var countClaimedToday = []string{
	"the published packages under core/",
	"the commerce modules under internal/modules",
	"the decision records under docs/adr",
	"the entries of docs/known-limits.md",
	"the groups of docs/known-limits.md",
}

// countClaimedElsewhere pins the totals stated OUTSIDE the two READMEs.
//
// They are pinned for a sharper reason than the README's. A total in a godoc is
// written inside a wrapped paragraph, and gofmt or an edit can move the number
// onto a different line from the noun — at which point the claim leaves this
// audit's population WITHOUT the sentence changing meaning to a reader. That is
// not hypothetical: all three of these were found stale on 2026-09-09 and two
// of them were invisible for exactly that reason, the count having wrapped away
// from its noun.
//
// So the list is the counterweight to the anchor rule. The anchor is what keeps
// subsets out; this is what stops a total from slipping out of scope on a
// reflow. A sentence that legitimately stops stating a total leaves this list in
// a diff somebody reads.
var countClaimedElsewhere = []struct{ file, population string }{
	{"internal/adminui/doc.go", "the commerce modules under internal/modules"},
	{"internal/arch/module_sql_test.go", "the in-tree plugins"},
	{"internal/smoke/process_test.go", "the commerce modules under internal/modules"},
	{"internal/smoke/race_test.go", "the commerce modules under internal/modules"},
}

// TestTheCountClaimScannerIsNotBlind pins down what [collectCountClaims] sees and,
// just as importantly, what it drops.
//
// Every door out of this audit is here: the vocabulary that can stop matching, the
// dated-record rule that can widen, the quoted-row rule, and the number reader that
// can stop reading a form. A vocabulary that quietly stops matching reports a clean
// tree it never read, which is the failure D23 records four times over.
func TestTheCountClaimScannerIsNotBlind(t *testing.T) {
	t.Parallel()

	found := map[string]bool{}
	for _, claim := range collectCountClaims(t) {
		found[claim.file+" :: "+claim.population] = true
	}

	for _, readme := range []string{"README.md"} {
		for _, population := range countClaimedToday {
			assert.True(t, found[readme+" :: "+population],
				"%s no longer prices %q anywhere this audit can see it.\n"+
					"Either the sentence was removed — then remove it from "+
					"countClaimedToday in the same diff, where somebody reads the "+
					"decision — or the anchor, the noun list or the number reader has "+
					"stopped matching it, and the count it states is no longer checked "+
					"by anything.", readme, population)
		}
	}

	for _, claim := range countClaimedElsewhere {
		assert.True(t, found[claim.file+" :: "+claim.population],
			"%s no longer prices %q anywhere this audit can see it.\n"+
				"In a godoc the usual cause is a REFLOW: the number and its noun ended "+
				"up on different lines, and this audit reads a line — so the sentence "+
				"still says the same thing to a reader and is no longer checked by "+
				"anything. Put the number, the noun and the path back on one line, or "+
				"remove the entry from countClaimedElsewhere.",
			claim.file, claim.population)
	}
}

// TestTheCountClaimExclusionsHold pins the two doors out of the audit to a shape
// rather than to a judgement.
func TestTheCountClaimExclusionsHold(t *testing.T) {
	t.Parallel()

	assert.True(t, countDatedRecord("CHANGELOG.md"), "the changelog is a dated record")
	assert.True(t, countDatedRecord(".claude/worktrees/wf_1/CHANGELOG.md"),
		"a nested checkout brought an excluded record back into scope through a longer "+
			"path, which would make this audit report defects the tree does not have")
	assert.True(t, countDatedRecord("docs/adr/0001-modul-arasi-iletisim.md"),
		"an ADR record is a dated record")
	assert.True(t, countDatedRecord(fmt.Sprintf("docs/adr/%04d-a-record-written-later.md", routeFrozenADR+9)),
		"a record written AFTER the route gate's frozen line is still a dated record "+
			"HERE: a count in it is a measurement taken on its date, and ADR 0069's is a "+
			"transition whose halves fall on different lines")

	assert.False(t, countDatedRecord("docs/adr/README.md"),
		"the ADR INDEX is not a record: it is rewritten whenever a record is added, and "+
			"an index miscounting its own rows is the defect this class was built for")
	assert.False(t, countDatedRecord("README.md"), "a README describes today")
	assert.False(t, countDatedRecord(knownLimitsDoc), "the limits document describes today")
	assert.False(t, countDatedRecord("docs/measurements/b2-remainder.md"),
		"a measurement is kept current — b2-remainder.md carries its own corrections — "+
			"and stays in scope")

	row := "| [0026](0026-the-published-surface-is-fourteen-packages.md) | fourteen packages |"
	assert.True(t, countQuotedRow("docs/adr/README.md", row),
		"a row of the index quotes the record it links to; the number in it is that "+
			"record's and not the index's")
	assert.False(t, countQuotedRow("docs/adr/README.md", "The index holds sixty-eight records."),
		"a line of the index that links NO record is the index speaking for itself and "+
			"stays in scope — an index miscounting its own rows is what this rule must "+
			"not forgive")
	assert.False(t, countQuotedRow("README.md", row),
		"the quoted-row rule belongs to the ADR index alone")

	for _, claim := range collectCountClaims(t) {
		assert.False(t, countDatedRecord(claim.file),
			"%s is a dated record, out of scope, and a claim was taken from it anyway",
			claim.file)
	}
}

// TestTheCountNumberReaderReadsBothForms pins the number reader to the forms this
// tree actually writes.
//
// The form was MEASURED before it was chosen: across the documents and the Go
// comments, a count under twenty is written as a WORD far more often than as a
// digit, the two Turkish files write theirs as Turkish words, and both compound
// forms occur — hyphenated in English, and written as two separate words in
// Turkish. A reader that handled digits alone would have found ZERO of the six
// live defects this gate opened with.
//
// The Turkish cases are built FROM the vocabulary rather than spelled here, for
// the reason [countNumeralsFile] carries, and building them proves the file is
// readable as well: a table that reads back nothing would fail the lookup below
// before it failed an assertion.
func TestTheCountNumberReaderReadsBothForms(t *testing.T) {
	t.Parallel()

	numerals := countNumeralWords(t)
	turkish := func(value int) string {
		for word, held := range numerals.units {
			if held == value && countEnglishUnits[word] == 0 {
				return word
			}
		}
		for word, held := range numerals.tens {
			if held == value && countEnglishTens[word] == 0 {
				return word
			}
		}
		require.Fail(t, "the vocabulary holds no Turkish word for %d", value)

		return ""
	}

	for _, testCase := range []struct {
		name   string
		tokens []string
		value  int
		span   int
	}{
		{"a digit", []string{"82"}, 82, 1},
		{"an English unit", []string{"seventeen"}, 17, 1},
		{"an English compound", []string{"fifty-one"}, 51, 1},
		{"an English round ten", []string{"ninety"}, 90, 1},
		{"a Turkish unit", []string{turkish(7)}, 7, 1},
		{"a Turkish teen", []string{numerals.turkishTen, turkish(7)}, 17, 2},
		{"a Turkish compound ending in one", []string{turkish(50), turkish(1)}, 51, 2},
		{"a Turkish compound with a diacritic", []string{"yirmi", "\u00FC\u00E7"}, 23, 2},
		{"a diacriticless Turkish compound", []string{turkish(20), turkish(3)}, 23, 2},
		{"capitalisation", []string{"Seventeen"}, 17, 1},
	} {
		value, span := numerals.at(testCase.tokens, 0)
		assert.Equal(t, testCase.value, value, "%s: %q", testCase.name, testCase.tokens)
		assert.Equal(t, testCase.span, span, "%s: the span of %q", testCase.name, testCase.tokens)
	}

	for _, testCase := range []struct {
		name   string
		tokens []string
	}{
		{"a record number", []string{"0026"}},
		{"a migration number", []string{"000003"}},
		{"a version", []string{"16.14"}},
		{"a path", []string{"docs/adr/0044-the-sales-channel-moves-into-the-catalog-path.md"}},
		{"the English preposition", []string{numerals.turkishTen, "both"}},
		{"the English preposition before a table", []string{numerals.turkishTen, "all"}},
		{"an ordinary word", []string{"modules"}},
		// The scale words. Each of these read as a number before the hard stop
		// went in, and every one of them read the WRONG number: "one hundred"
		// as 1, "one hundred and five" as 5. A reader that cannot compose a
		// value is required to stay silent, not to answer with the part it
		// understood.
		{"a bare scale word", []string{"hundred"}},
		{"a number in front of a scale", []string{"one", "hundred"}},
		{"a digit in front of a scale", []string{"5", "thousand"}},
		{"the English hundreds compound", []string{"one", "hundred", "and", "five"}},
	} {
		value, _ := numerals.at(testCase.tokens, 0)
		assert.Equal(t, -1, value, "%s: %q was read as a number", testCase.name, testCase.tokens)
	}

	// The Turkish scales are taken from the vocabulary rather than written
	// here, so this stays true of whatever the data file holds and no Turkish
	// word enters the source to say it.
	require.NotEmpty(t, numerals.turkishScales)

	for _, scale := range numerals.turkishScales {
		for _, tokens := range [][]string{
			{scale},
			{turkish(2), scale},
			{"7", scale},
		} {
			value, _ := numerals.at(tokens, 0)
			assert.Equal(t, -1, value,
				"a Turkish scale: %q was read as a number", tokens)
		}
	}
}

// TestTheCountClaimShapeBitesAndStops pins the claim shape to the sentences it must
// read and the ones it must leave alone.
//
// The negative cases are not hypothetical. Every one of them is a real line from
// this tree that an earlier, wider shape read as a claim; they are what the path
// tokenizer, the function-word gap and the single-segment anchor rule were each
// added for, and a change that loses one of those brings its false claim back.
func TestTheCountClaimShapeBitesAndStops(t *testing.T) {
	t.Parallel()

	numerals := countNumeralWords(t)

	modules := countedPopulations[0]
	require.Equal(t, modulesDir, modules.anchor, "the first entry is the module population")

	core := countedPopulations[1]
	require.Equal(t, "core", core.anchor, "the second entry is the published packages")

	found := countClaimsIn(
		"internal/modules      # seventeen isolated commerce modules (product, pricing,", modules, numerals)
	require.Len(t, found, 1, "the layout block's own line is the shape this audit exists for")
	assert.Equal(t, 17, found[0].stated)

	found = countClaimsIn(
		"internal/modules      # on yedi izole commerce mod\u00FCl\u00FC (product, pricing,", modules, numerals)
	require.Len(t, found, 1, "the Turkish layout line is the same claim in the other language")
	assert.Equal(t, 17, found[0].stated, "the Turkish compound was not read as a whole")

	found = countClaimsIn("core                  # the PUBLISHED contracts — sixteen packages (ADR 0026):", core, numerals)
	require.Len(t, found, 1, "a single-segment anchor is read in the layout column")
	assert.Equal(t, 16, found[0].stated)

	for _, line := range []string{
		"// importing the four core packages to compare two strings that the round trip",
		"// import both the core and the modules; neither of the two packages can import",
		"// twelve core packages out of internal/ (ADR 0026) added a tree, and every one",
	} {
		assert.Empty(t, countClaimsIn(line, core, numerals),
			"%q is a SUBSET of core/, not its size, and it anchors only if a bare "+
				"\"core\" is allowed to name the tree", line)
	}

	for _, line := range []string{
		"`internal/modules/order/module.go`, and if the b2b module is not installed the",
		"estimated. Thirteen surfaces, twelve under `internal/modules` and one in a",
		"One file: `internal/modules/invoice/migrations/000001_invoice_init.up.sql`,",
		"1. Under `internal/modules/<name>/`: `module.go`, `models`, `repository`,",
	} {
		assert.Empty(t, countClaimsIn(line, modules, numerals),
			"%q was read as a claim about the module population and is not one", line)
	}

	limits := countedPopulations[6]
	require.Equal(t, knownLimitsDoc, limits.anchor, "the seventh entry is the limits document")
	found = countClaimsIn(
		"| [`docs/known-limits.md`](./docs/known-limits.md) | twenty-one items in four groups |", limits, numerals)
	require.Len(t, found, 1,
		"the first noun ends the claim: \"twenty-one items in four groups\" prices the "+
			"items with twenty-one, and the groups are a claim of their own")
	assert.Equal(t, 21, found[0].stated)
}
