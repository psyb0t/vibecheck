package policy

import (
	"sort"
	"strings"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
)

// rulePhase distinguishes the two rule lists. It decides whether an answer
// reference is legal: a pre-rule runs before the provider is called, so it can
// only see facts.
type rulePhase string

const (
	rulePhasePre      rulePhase = "preRules"
	rulePhaseDecision rulePhase = "decisionRules"
)

func (p rulePhase) allowsAnswers() bool {
	return p == rulePhaseDecision
}

// compiler carries the declarations resolved so far. Each stage fills in the
// part the next stage needs to resolve references against.
type compiler struct {
	doc          *Document
	maxQuestions int

	outcomes  map[decision.Outcome]struct{}
	facts     map[string]decision.FactType
	questions map[string]CompiledQuestion
	ruleIDs   map[string]struct{}
}

// Compile turns a policy document into an evaluatable policy, rejecting
// anything it cannot fully resolve and type-check.
//
// Every failure wraps one of this package's sentinels so a caller can
// distinguish a malformed document from an unsupported schema version without
// parsing the message.
func Compile(doc *Document) (*Compiled, error) {
	return CompileWithMaxQuestions(doc, MaxQuestions)
}

// CompileWithMaxQuestions compiles a policy with a deployment-specific
// question ceiling. The package maximum remains an absolute upper bound.
func CompileWithMaxQuestions(
	doc *Document,
	maxQuestions int,
) (*Compiled, error) {
	if err := validateDocumentIdentity(doc); err != nil {
		return nil, err
	}

	comp := &compiler{
		doc:          doc,
		maxQuestions: resolveMaxQuestions(maxQuestions),
		outcomes:     map[decision.Outcome]struct{}{},
		facts:        map[string]decision.FactType{},
		questions:    map[string]CompiledQuestion{},
		ruleIDs:      map[string]struct{}{},
	}

	compiled := &Compiled{
		Description: doc.Metadata.Description,
		Model:       doc.Spec.Model,
		StrictInput: doc.Spec.Input.Strict,
	}

	if err := comp.compileStages(doc, compiled); err != nil {
		return nil, err
	}

	compiled.outcomeSet = comp.outcomes

	canonical, hash, err := canonicalize(compiled)
	if err != nil {
		return nil, err
	}

	compiled.Canonical = canonical
	compiled.Hash = hash

	return compiled, nil
}

// validateDocumentIdentity checks that doc declares the API version and kind
// this build compiles. Every other document shape is Compile's job to
// reject; this only guards the identity a future schema revision would
// change first.
func validateDocumentIdentity(doc *Document) error {
	if doc == nil {
		return ctxerrors.Wrap(ErrInvalidDocument, "document is nil")
	}

	if doc.APIVersion != APIVersion {
		return ctxerrors.Wrapf(
			ErrUnsupportedAPIVersion,
			"got %q, this build compiles %q", doc.APIVersion, APIVersion,
		)
	}

	if doc.Kind != Kind {
		return ctxerrors.Wrapf(
			ErrUnsupportedKind, "got %q, want %q", doc.Kind, Kind,
		)
	}

	return nil
}

// compileStages runs every compilation phase in dependency order, filling
// compiled with fully resolved outcomes, facts, questions, and rules.
func (c *compiler) compileStages(doc *Document, compiled *Compiled) error {
	if err := c.compileMetadata(compiled); err != nil {
		return err
	}

	if err := c.compileOutcomes(compiled); err != nil {
		return err
	}

	if err := c.compileFacts(compiled); err != nil {
		return err
	}

	if err := c.compileQuestions(compiled); err != nil {
		return err
	}

	preRules, err := c.compileRules(doc.Spec.PreRules, rulePhasePre)
	if err != nil {
		return err
	}

	decisionRules, err := c.compileRules(
		doc.Spec.DecisionRules, rulePhaseDecision,
	)
	if err != nil {
		return err
	}

	compiled.PreRules = preRules
	compiled.DecisionRules = decisionRules

	return nil
}

func (c *compiler) compileMetadata(out *Compiled) error {
	name := c.doc.Metadata.Name
	if !decision.IsDashedIdentifier(name) {
		return ctxerrors.Wrapf(
			ErrInvalidMetadata,
			"name %q must match the bounded identifier grammar", name,
		)
	}

	version := c.doc.Metadata.Version
	if version == "" || len(version) > MaxVersionLength {
		return ctxerrors.Wrapf(
			ErrInvalidMetadata,
			"version must be 1 to %d characters", MaxVersionLength,
		)
	}

	if strings.TrimSpace(version) != version {
		return ctxerrors.Wrap(
			ErrInvalidMetadata, "version must not have surrounding whitespace",
		)
	}

	if len(c.doc.Metadata.Description) > MaxDescriptionLength {
		return ctxerrors.Wrapf(
			ErrInvalidMetadata,
			"description must be at most %d characters", MaxDescriptionLength,
		)
	}

	out.Ref = Ref{Name: name, Version: version}

	return nil
}

func (c *compiler) compileOutcomes(out *Compiled) error {
	declared := c.doc.Spec.Outcomes
	if len(declared) == 0 {
		return ctxerrors.Wrap(
			ErrInvalidOutcome, "at least one outcome is required",
		)
	}

	if len(declared) > MaxOutcomes {
		return ctxerrors.Wrapf(
			ErrInvalidOutcome, "at most %d outcomes are allowed", MaxOutcomes,
		)
	}

	for _, outcome := range declared {
		if !outcome.IsValid() {
			return ctxerrors.Wrapf(
				ErrInvalidOutcome,
				"outcome %q must match the bounded identifier grammar", outcome,
			)
		}

		if _, exists := c.outcomes[outcome]; exists {
			return ctxerrors.Wrapf(
				ErrInvalidOutcome, "outcome %q is declared twice", outcome,
			)
		}

		c.outcomes[outcome] = struct{}{}
	}

	defaultOutcome := c.doc.Spec.DefaultOutcome
	if _, ok := c.outcomes[defaultOutcome]; !ok {
		return ctxerrors.Wrapf(
			ErrInvalidOutcome,
			"defaultOutcome %q is not in the declared outcome set",
			defaultOutcome,
		)
	}

	out.Outcomes = append([]decision.Outcome(nil), declared...)
	out.DefaultOutcome = defaultOutcome

	return nil
}

func (c *compiler) compileFacts(out *Compiled) error {
	required := c.doc.Spec.Input.RequiredFacts
	optional := c.doc.Spec.Input.OptionalFacts

	if len(required)+len(optional) > MaxRequiredFacts {
		return ctxerrors.Wrapf(
			ErrInvalidFact,
			"at most %d declared facts are allowed", MaxRequiredFacts,
		)
	}

	out.RequiredFacts = map[string]decision.FactType{}
	out.OptionalFacts = map[string]decision.FactType{}

	if err := c.collectFacts(required, out.RequiredFacts); err != nil {
		return err
	}

	if err := c.collectFacts(optional, out.OptionalFacts); err != nil {
		return err
	}

	return nil
}

func (c *compiler) collectFacts(
	source map[string]decision.FactType,
	target map[string]decision.FactType,
) error {
	for _, name := range sortedFactNames(source) {
		factType := source[name]

		if !decision.IsFactName(name) {
			return ctxerrors.Wrapf(
				ErrInvalidFact,
				"fact name %q must match the bounded fact grammar", name,
			)
		}

		if !factType.IsValid() {
			return ctxerrors.Wrapf(
				ErrInvalidFact,
				"fact %q has unsupported type %q", name, factType,
			)
		}

		if _, exists := c.facts[name]; exists {
			return ctxerrors.Wrapf(
				ErrInvalidFact,
				"fact %q is declared both required and optional", name,
			)
		}

		c.facts[name] = factType
		target[name] = factType
	}

	return nil
}

func (c *compiler) compileQuestions(out *Compiled) error {
	declared := c.doc.Spec.Questions
	if len(declared) == 0 {
		return ctxerrors.Wrap(
			ErrInvalidQuestion, "at least one question is required",
		)
	}

	if len(declared) > c.maxQuestions {
		return ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"at most %d questions are allowed", c.maxQuestions,
		)
	}

	ids := make([]string, 0, len(declared))
	for id := range declared {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	for _, id := range ids {
		question, err := c.compileQuestion(id, declared[id])
		if err != nil {
			return err
		}

		c.questions[id] = question
	}

	out.Questions = c.questions
	out.QuestionIDs = ids

	return nil
}

func resolveMaxQuestions(maxQuestions int) int {
	if maxQuestions <= 0 || maxQuestions > MaxQuestions {
		return MaxQuestions
	}

	return maxQuestions
}

func (c *compiler) compileQuestion(
	id string,
	question Question,
) (CompiledQuestion, error) {
	if !decision.IsIdentifier(id) {
		return CompiledQuestion{}, ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"question ID %q must match the bounded identifier grammar", id,
		)
	}

	if !question.Type.IsValid() {
		return CompiledQuestion{}, ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"question %q has unsupported type %q", id, question.Type,
		)
	}

	instructions, err := compileQuestionInstructions(id, question.Instructions)
	if err != nil {
		return CompiledQuestion{}, err
	}

	compiled := CompiledQuestion{
		ID:           id,
		Type:         question.Type,
		Instructions: instructions,
	}

	return compileQuestionCriteria(id, question, compiled)
}

// compileQuestionInstructions normalizes and bounds a question's
// instructions text, which every question type requires regardless of its
// criteria shape.
func compileQuestionInstructions(id, raw string) (string, error) {
	instructions := strings.TrimSpace(raw)
	if instructions == "" {
		return "", ctxerrors.Wrapf(
			ErrInvalidQuestion, "question %q has empty instructions", id,
		)
	}

	if len(instructions) > MaxInstructionsLength {
		return "", ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"question %q instructions exceed %d characters",
			id, MaxInstructionsLength,
		)
	}

	return instructions, nil
}

// compileQuestionCriteria resolves the type-specific criteria shape: absent
// for noul, an option-to-description map for choice, an ordered level list
// for score.
func compileQuestionCriteria(
	id string,
	question Question,
	compiled CompiledQuestion,
) (CompiledQuestion, error) {
	switch question.Type {
	case decision.QuestionTypeNoul:
		if question.Criteria != nil {
			return CompiledQuestion{}, ctxerrors.Wrapf(
				ErrInvalidQuestion,
				"noul question %q must not declare criteria", id,
			)
		}

		return compiled, nil

	case decision.QuestionTypeChoice:
		criteria, keys, err := compileChoiceCriteria(id, question.Criteria)
		if err != nil {
			return CompiledQuestion{}, err
		}

		compiled.ChoiceCriteria = criteria
		compiled.ChoiceKeys = keys

		return compiled, nil

	case decision.QuestionTypeScore:
		levels, err := compileScoreCriteria(id, question.Criteria)
		if err != nil {
			return CompiledQuestion{}, err
		}

		compiled.ScoreCriteria = levels

		return compiled, nil
	}

	return CompiledQuestion{}, ctxerrors.Wrapf(
		ErrInvalidQuestion,
		"question %q has unsupported type %q", id, question.Type,
	)
}

func compileChoiceCriteria(
	id string,
	raw any,
) (map[string]string, []string, error) {
	source, ok := raw.(map[string]any)
	if !ok {
		return nil, nil, ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"choice question %q needs criteria as an option-to-description map",
			id,
		)
	}

	if len(source) < MinChoiceCriteria || len(source) > MaxChoiceCriteria {
		return nil, nil, ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"choice question %q needs %d to %d options, got %d",
			id, MinChoiceCriteria, MaxChoiceCriteria, len(source),
		)
	}

	criteria := make(map[string]string, len(source))
	keys := make([]string, 0, len(source))

	for key := range source {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		if err := validateChoiceOptionKey(id, key); err != nil {
			return nil, nil, err
		}

		description, err := criteriaText(id, key, source[key])
		if err != nil {
			return nil, nil, err
		}

		criteria[key] = description
	}

	return criteria, keys, nil
}

// validateChoiceOptionKey checks one choice option key against the bounded
// identifier grammar every policy identifier must match.
func validateChoiceOptionKey(id, key string) error {
	if !decision.IsIdentifier(key) {
		return ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"question %q option %q must match the bounded identifier grammar",
			id, key,
		)
	}

	return nil
}

// criteriaText normalizes one choice description. TypeSafe allows a null
// description for an option that needs no extra detail, so nil maps to the
// empty string rather than failing.
func criteriaText(id, key string, raw any) (string, error) {
	if raw == nil {
		return "", nil
	}

	text, ok := raw.(string)
	if !ok {
		return "", ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"choice question %q option %q description must be a string or null",
			id, key,
		)
	}

	if len(text) > MaxCriteriaTextLength {
		return "", ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"choice question %q option %q description exceeds %d characters",
			id, key, MaxCriteriaTextLength,
		)
	}

	return text, nil
}

func compileScoreCriteria(id string, raw any) ([]string, error) {
	source, ok := raw.([]any)
	if !ok {
		return nil, ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"score question %q needs an ordered list of level descriptions",
			id,
		)
	}

	if len(source) < MinScoreCriteria || len(source) > MaxScoreCriteria {
		return nil, ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"score question %q needs %d to %d levels, got %d",
			id, MinScoreCriteria, MaxScoreCriteria, len(source),
		)
	}

	levels := make([]string, 0, len(source))

	for index, entry := range source {
		text, ok := entry.(string)
		if !ok {
			return nil, ctxerrors.Wrapf(
				ErrInvalidQuestion,
				"score question %q level %d must be a string", id, index,
			)
		}

		text = strings.TrimSpace(text)
		if text == "" {
			return nil, ctxerrors.Wrapf(
				ErrInvalidQuestion,
				"score question %q level %d is empty", id, index,
			)
		}

		if len(text) > MaxCriteriaTextLength {
			return nil, ctxerrors.Wrapf(
				ErrInvalidQuestion,
				"score question %q level %d exceeds %d characters",
				id, index, MaxCriteriaTextLength,
			)
		}

		levels = append(levels, text)
	}

	return levels, nil
}

func (c *compiler) compileRules(
	rules []Rule,
	phase rulePhase,
) ([]CompiledRule, error) {
	if len(rules) > MaxRules {
		return nil, ctxerrors.Wrapf(
			ErrInvalidRule, "%s: at most %d rules are allowed", phase, MaxRules,
		)
	}

	compiled := make([]CompiledRule, 0, len(rules))
	sawUnconditional := false

	for index, rule := range rules {
		if sawUnconditional {
			return nil, ctxerrors.Wrapf(
				ErrInvalidRule,
				"%s: rule %q is unreachable; an earlier rule is unconditional",
				phase, rule.ID,
			)
		}

		entry, err := c.compileRule(rule, phase, index)
		if err != nil {
			return nil, err
		}

		if entry.Condition == nil {
			sawUnconditional = true
		}

		compiled = append(compiled, entry)
	}

	return compiled, nil
}

func (c *compiler) compileRule(
	rule Rule,
	phase rulePhase,
	index int,
) (CompiledRule, error) {
	if !decision.IsDashedIdentifier(rule.ID) {
		return CompiledRule{}, ctxerrors.Wrapf(
			ErrInvalidRule,
			"%s[%d]: rule ID %q must match the bounded identifier grammar",
			phase, index, rule.ID,
		)
	}

	if _, exists := c.ruleIDs[rule.ID]; exists {
		return CompiledRule{}, ctxerrors.Wrapf(
			ErrInvalidRule, "rule ID %q is declared twice", rule.ID,
		)
	}

	c.ruleIDs[rule.ID] = struct{}{}

	if len(rule.Description) > MaxDescriptionLength {
		return CompiledRule{}, ctxerrors.Wrapf(
			ErrInvalidRule,
			"rule %q description exceeds %d characters",
			rule.ID, MaxDescriptionLength,
		)
	}

	if _, ok := c.outcomes[rule.Outcome]; !ok {
		return CompiledRule{}, ctxerrors.Wrapf(
			ErrInvalidOutcome,
			"rule %q selects outcome %q which is not declared",
			rule.ID, rule.Outcome,
		)
	}

	entry := CompiledRule{
		ID:          rule.ID,
		Description: rule.Description,
		Outcome:     rule.Outcome,
	}

	if rule.When == nil {
		return entry, nil
	}

	condition, err := c.compileCondition(*rule.When, phase, rule.ID, 0)
	if err != nil {
		return CompiledRule{}, err
	}

	entry.Condition = &condition

	return entry, nil
}
