---
name: plan-review
description: Reviews implementation plans through a multi-round, multi-agent workflow before coding begins. Use when validating a plan, pressure-testing task sequencing, checking codebase fit, or preparing a revise/accept/reject recommendation for any repository.
---

# Plan Review

Run a four-round plan review that stress-tests a proposed implementation plan against the actual codebase before execution starts.

## When to Use

- The user provides a draft plan and wants a deep review
- A plan looks plausible but may be incomplete, incorrect, or risky
- You need an evidence-backed accept/revise/reject recommendation
- You want parallel codebase research before implementation begins

## Required Inputs

Before dispatching any agents, prepare these two blocks and inject them into every prompt template:

- `[INSERT PLAN TEXT]` — the full plan to review
- `[INSERT CODEBASE CONTEXT]` — concise repository-specific context supplied by the invoking agent, such as architecture, key directories, languages, frameworks, known constraints, and relevant commands

If either block is incomplete, gather the missing context first. Do not ask sub-agents to infer repository context that you already can provide.

## Operating Rules

1. All review agents must be `explore` agents.
2. Dispatch all agents in a round in parallel.
3. Use `mode: "background"` for every sub-agent.
4. Treat all sub-agents as read-only. They review, search, and analyze; they do not edit files.
5. Do not start the next round until you consolidate the current round.
6. Every sub-agent prompt must be self-contained because sub-agents are stateless.
7. If a sub-agent has questions, it must return them in its output. Sub-agents cannot talk to users directly.

## Finding Format

Require every sub-agent to report findings in this format:

```text
FINDING: <short title or NONE>
STATUS: CONFIRMED | PARTIAL | REJECTED | QUESTION
SEVERITY: 🔴 | 🟠 | 🟡 | 🔵
EVIDENCE:
- <file/path:line or command/result>
- <file/path:line or command/result>
DETAIL:
- <concise explanation of the issue, risk, or suggestion>
- <why it matters to the plan>
QUESTIONS:
- <question for the invoking agent to resolve, if any>
```

### Severity Categories

- 🔴 **Plan Would Cause Harm** — the plan would likely break behavior, violate constraints, or introduce major risk
- 🟠 **Plan Claims Are Wrong** — the plan asserts something incorrect about the codebase or approach
- 🟡 **Plan Missed Something** — the plan is incomplete, missing dependencies, tests, migration steps, edge cases, or coordination work
- 🔵 **Suggestions** — optional improvements, refinements, or clearer alternatives

## Workflow

### Step 0: Prepare the Review Packet

Create a short review packet for all prompts:

- Objective of the plan
- `[INSERT PLAN TEXT]`
- `[INSERT CODEBASE CONTEXT]`
- Any explicit constraints from the user
- Instructions to use the finding format above

Also tell each sub-agent:

- cite evidence precisely
- distinguish verified facts from speculation
- return `FINDING: NONE` if no material issue is found
- include unresolved questions in `QUESTIONS:`

---

## Round 1 — Discovery

Launch these three `explore` agents in parallel, all with `mode: "background"`:

### Agent 1A — Fact Checker

Purpose: verify every specific claim, assumption, and codebase statement in the plan against the actual repository.

#### Prompt Template

```text
You are Agent 1A, the Fact Checker in a multi-round plan review workflow.

Review this implementation plan against the repository. Verify every concrete claim in the plan against actual code, configuration, and project structure.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Tasks:
1. Extract every specific technical claim from the plan.
2. Verify whether each claim matches the real codebase.
3. Flag incorrect assumptions, missing files, wrong ownership, or unsupported approaches.
4. Return only evidence-backed findings.
5. If a claim cannot be verified, mark it as PARTIAL or QUESTION rather than guessing.

Use this exact output format for every issue:
FINDING / STATUS / SEVERITY / EVIDENCE / DETAIL / QUESTIONS

Severity reminder:
- 🔴 harmful plan outcome
- 🟠 wrong claim
- 🟡 missing item
- 🔵 suggestion

Sub-agents cannot ask the user directly. Put any needed clarifications under QUESTIONS.
```

### Agent 1B — Completeness Auditor

Purpose: find gaps, blind spots, and omitted work the plan fails to cover.

#### Prompt Template

```text
You are Agent 1B, the Completeness Auditor in a multi-round plan review workflow.

Review this implementation plan for omissions. Search the repository for required work the plan did not mention.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Tasks:
1. Identify missing implementation steps, integration points, migrations, configs, tests, docs, rollout steps, or operational concerns.
2. Look for edge cases, cross-cutting concerns, and adjacent modules the plan should include.
3. Prefer findings that materially affect correctness, delivery order, or maintenance risk.
4. Distinguish mandatory gaps from optional improvements.

Use this exact output format for every issue:
FINDING / STATUS / SEVERITY / EVIDENCE / DETAIL / QUESTIONS

Sub-agents cannot ask the user directly. Put any needed clarifications under QUESTIONS.
```

### Agent 1C — Feasibility Assessor

Purpose: assess whether the proposed solution is technically sound and practical in this codebase.

#### Prompt Template

```text
You are Agent 1C, the Feasibility Assessor in a multi-round plan review workflow.

Evaluate whether this implementation plan is feasible and architecturally sound in the actual repository.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Tasks:
1. Evaluate whether the proposed changes fit the current architecture, abstractions, and constraints.
2. Flag steps that are underspecified, unrealistic, unsafe, or likely to fail during implementation.
3. Identify cleaner or lower-risk approaches when the plan's approach is weak.
4. Focus on technical feasibility, not stylistic preferences.

Use this exact output format for every issue:
FINDING / STATUS / SEVERITY / EVIDENCE / DETAIL / QUESTIONS

Sub-agents cannot ask the user directly. Put any needed clarifications under QUESTIONS.
```

### Round 1 Consolidation

After all three agents finish:

1. Merge duplicate findings.
2. Split findings into:
   - verified defects in the plan
   - likely gaps needing deeper validation
   - open questions
3. Create a shortlist of the most impactful findings to challenge in Round 2.
4. Preserve all cited evidence for reuse.

---

## Round 2 — Challenge

Launch these three `explore` agents in parallel, all with `mode: "background"`:

### Agent 2A — Devil's Advocate

Purpose: challenge Round 1 findings and eliminate weak, overstated, or false-positive concerns.

#### Prompt Template

```text
You are Agent 2A, the Devil's Advocate in a multi-round plan review workflow.

Challenge the current review findings. Your job is to disprove weak claims, catch overreach, and identify where the earlier reviewers may have overestimated risk.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Round 1 findings to challenge:
[INSERT ROUND 1 CONSOLIDATED FINDINGS]

Tasks:
1. Re-check the strongest Round 1 claims.
2. Identify false positives, nuance, or missing context that weakens them.
3. Confirm which findings remain solid after scrutiny.
4. Return both upheld and rebutted findings with evidence.

Use this exact output format for every issue:
FINDING / STATUS / SEVERITY / EVIDENCE / DETAIL / QUESTIONS
```

### Agent 2B — Dependency Analyst

Purpose: validate task ordering, prerequisites, and dependency correctness inside the plan.

#### Prompt Template

```text
You are Agent 2B, the Dependency Analyst in a multi-round plan review workflow.

Analyze task ordering and dependencies in the plan.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Round 1 findings:
[INSERT ROUND 1 CONSOLIDATED FINDINGS]

Tasks:
1. Check whether the plan's step order is correct.
2. Identify hidden prerequisites, blockers, sequencing errors, or parallelization mistakes.
3. Verify whether tests, migrations, contracts, and rollout dependencies are placed correctly.
4. Suggest safer ordering when the current order is wrong.

Use this exact output format for every issue:
FINDING / STATUS / SEVERITY / EVIDENCE / DETAIL / QUESTIONS
```

### Agent 2C — Codebase Deep Diver

Purpose: deeply re-verify the top five most impactful Round 1 findings.

#### Prompt Template

```text
You are Agent 2C, the Codebase Deep Diver in a multi-round plan review workflow.

Deep-verify the highest-impact findings from Round 1 by tracing the repository in more depth.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Top findings to verify:
[INSERT TOP 5 ROUND 1 FINDINGS]

Tasks:
1. Perform deep evidence gathering on the top five findings.
2. Trace the actual code paths, ownership boundaries, and integration points involved.
3. Upgrade weak evidence into strong evidence, or downgrade findings that do not hold up.
4. Focus on the most consequential issues only.

Use this exact output format for every issue:
FINDING / STATUS / SEVERITY / EVIDENCE / DETAIL / QUESTIONS
```

### Round 2 Consolidation

After all three agents finish:

1. Remove or downgrade findings invalidated by Agent 2A.
2. Add confirmed sequencing and dependency issues from Agent 2B.
3. Replace shallow evidence with deep evidence from Agent 2C.
4. Produce a clean set of validated findings for prioritization.

---

## Round 3 — Convergence

Launch these three `explore` agents in parallel, all with `mode: "background"`:

### Agent 3A — Priority Scorer

Purpose: assign confidence and priority scores and bucket findings by severity.

#### Prompt Template

```text
You are Agent 3A, the Priority Scorer in a multi-round plan review workflow.

Score and categorize the validated findings.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Validated findings:
[INSERT ROUND 2 VALIDATED FINDINGS]

Tasks:
1. Assign each finding a confidence score from 1-5.
2. Assign each finding a priority score from 1-5.
3. Categorize each finding as 🔴, 🟠, 🟡, or 🔵.
4. Explain the score briefly with evidence awareness.

Use the standard finding format and include confidence=<1-5> and priority=<1-5> inside DETAIL.
```

### Agent 3B — Action Planner

Purpose: turn validated findings into concrete plan revisions.

#### Prompt Template

```text
You are Agent 3B, the Action Planner in a multi-round plan review workflow.

Convert validated findings into specific revisions to the plan.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Validated findings:
[INSERT ROUND 2 VALIDATED FINDINGS]

Tasks:
1. Propose concrete edits to the plan, not generic advice.
2. Show what to add, remove, reorder, or rewrite.
3. Prefer minimal changes that resolve the highest-risk issues first.
4. Group revisions by the plan step they affect.

Format each revision as:
- PLAN DIFF: <step or section>
  - CHANGE: <add/remove/rewrite/reorder>
  - REASON: <why>
  - LINKED FINDINGS: <finding titles>
```

### Agent 3C — Meta Reviewer

Purpose: judge overall plan quality and produce a tentative recommendation.

#### Prompt Template

```text
You are Agent 3C, the Meta Reviewer in a multi-round plan review workflow.

Assess the overall quality of the plan after Round 2 validation.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Validated findings:
[INSERT ROUND 2 VALIDATED FINDINGS]

Tasks:
1. Grade the plan quality from 1 to 10.
2. Judge whether the plan should currently be ACCEPT, REVISE, or REJECT.
3. Explain the threshold that drove the recommendation.
4. Highlight the minimum revisions needed to move the plan to ACCEPT if possible.

Return:
- PLAN SCORE: <1-10>
- TENTATIVE RECOMMENDATION: ✅ ACCEPT | 🔧 REVISE | ❌ REJECT
- RATIONALE: <short explanation>
- TOP RISKS:
  - <risk>
```

### Round 3 Consolidation

After all three agents finish:

1. Attach confidence and priority scores to every validated finding.
2. Merge the plan revision diffs into a single revision list.
3. Record the Meta Reviewer score and tentative recommendation.
4. Identify any contradictions that must be reconciled in Round 4.

---

## Round 4 — Final Reconciliation

Launch these three `explore` agents in parallel, all with `mode: "background"`:

### Agent 4A — Consistency Checker

Purpose: detect contradictions across findings, scores, and proposed revisions.

#### Prompt Template

```text
You are Agent 4A, the Consistency Checker in a multi-round plan review workflow.

Check the full review package for internal contradictions.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Round 3 package:
[INSERT ROUND 3 CONSOLIDATED PACKAGE]

Tasks:
1. Find contradictions between findings, scores, and recommended revisions.
2. Flag duplicate findings with different severities or confidence levels.
3. Identify any revision that does not match its evidence.
4. Recommend reconciliation where needed.

Use the standard finding format.
```

### Agent 4B — Evidence Auditor

Purpose: audit evidence quality for the most severe findings.

#### Prompt Template

```text
You are Agent 4B, the Evidence Auditor in a multi-round plan review workflow.

Audit the evidence quality for every CRITICAL/HIGH-impact issue, especially 🔴 and strong 🟠 findings.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Round 3 package:
[INSERT ROUND 3 CONSOLIDATED PACKAGE]

Tasks:
1. Re-check evidence quality for the highest-impact findings.
2. Downgrade any finding that lacks sufficient direct evidence.
3. Confirm which severe findings are truly well supported.
4. Call out weak citations, missing traces, or speculative leaps.

Use the standard finding format.
```

### Agent 4C — Report Quality Gate

Purpose: calibrate the final report and ensure it is decision-ready.

#### Prompt Template

```text
You are Agent 4C, the Report Quality Gate in a multi-round plan review workflow.

Review the review itself. Your job is to ensure the final report is decision-ready, calibrated, and concise.

Plan:
[INSERT PLAN TEXT]

Codebase context:
[INSERT CODEBASE CONTEXT]

Round 3 package:
[INSERT ROUND 3 CONSOLIDATED PACKAGE]

Tasks:
1. Check whether the proposed report overstates or understates risk.
2. Ensure the most important findings appear first.
3. Confirm the recommendation matches the evidence strength.
4. Suggest any final wording or structure fixes for the report.

Use the standard finding format plus a final report-readiness verdict.
```

### Round 4 Consolidation

After all three agents finish:

1. Resolve contradictions flagged by Agent 4A.
2. Downgrade or remove severe findings that fail Agent 4B's evidence audit.
3. Apply calibration improvements from Agent 4C.
4. Produce the final recommendation and report.

---

## Final Report Template

Use this exact report structure:

```markdown
# Plan Review Report

## Recommendation
✅ ACCEPT | 🔧 REVISE | ❌ REJECT

## Executive Summary
- Plan objective: <one sentence>
- Overall assessment: <one short paragraph>
- Plan quality score: <1-10>

## Top Findings
| Severity | Finding | Confidence | Priority | Status |
|----------|---------|------------|----------|--------|
| 🔴/🟠/🟡/🔵 | <title> | <1-5> | <1-5> | <CONFIRMED/PARTIAL/REJECTED> |

## Detailed Findings
### <Finding Title>
FINDING: <title>
STATUS: <status>
SEVERITY: <severity>
EVIDENCE:
- <file/path:line or other evidence>
DETAIL:
- <what is wrong or missing>
- <impact on the plan>
QUESTIONS:
- <if any>

## Recommended Plan Revisions
- PLAN DIFF: <step or section>
  - CHANGE: <add/remove/rewrite/reorder>
  - REASON: <why>
  - LINKED FINDINGS: <finding titles>

## Dependency and Ordering Notes
- <sequencing issue or confirmation>

## Open Questions
- <questions that require user or implementer clarification>

## Review Coverage
- Round 1: Discovery complete/incomplete
- Round 2: Challenge complete/incomplete
- Round 3: Convergence complete/incomplete
- Round 4: Final reconciliation complete/incomplete
- Agent failures/timeouts: <none or list>

## Decision Rationale
- Why this recommendation is appropriate now
- What would be required to change the recommendation
```

## Recommendation Rules

- **✅ ACCEPT** — only when no unresolved 🔴 findings remain and any 🟠/🟡 items are minor enough not to block execution
- **🔧 REVISE** — when the plan is salvageable but requires meaningful corrections, additions, or reordering
- **❌ REJECT** — when core assumptions are wrong, the approach is unsafe, or the plan would likely fail without a major rewrite

## Error Handling

- If one sub-agent fails or times out, retry once with the same scope and a narrower prompt.
- If the retry also fails, continue the workflow and mark that coverage gap in `Review Coverage`.
- If multiple sub-agents in the same round fail, do not fabricate certainty. Lower confidence, surface the missing coverage, and avoid `✅ ACCEPT` unless the remaining evidence is overwhelming.
- If a later round depends on missing output, pass forward the best available consolidated findings and explicitly label the blind spot.
- If one agent returns only questions, treat that as usable output and carry those questions into the final report.

## Execution Notes

- Keep prompts self-contained; never assume a sub-agent remembers prior rounds.
- Prefer concise, evidence-rich findings over long prose.
- Merge duplicates aggressively before handing findings to the next round.
- Preserve dissenting views when they materially affect confidence or recommendation.
- The invoking agent owns all user communication, final synthesis, and any follow-up edits to the plan.
