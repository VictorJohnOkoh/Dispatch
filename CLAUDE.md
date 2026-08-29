# Dispatch — Agent Guide
I am Victor, a second year Computer Science and AI student and I am tryign to improve my architecture design and software engineering skills 
I like simple solutions and understandable code
Make sure that when deciding with how you write out feature that you always choose the more efficient and imple to understand and maintain option
When choosing a data structure I prtefer using only what is necessary. 
Example:
- Using a hash map when the data needed is a known certain amount and the data structure is expected to be fully populated. <br>
Comments are very nice but they should be used sparingly and only when they add value. <br>
Good Example:
- For a function that adds 3 numbers together should be commented with "# adds 2 numbers together and returns the sum" <br>
Bad Example:
- For a function that adds 3 numbers together "Takes in 3 integers, performs the addition operation and returns the result as an integer" 
## Speech Pattern
When speaking always talk in ASD-STE100 Simplified Technical English, read CONTEXT.md and use the ubiquitous language.
Dumb down complex topics/options afterwards by describing them using more colloquial language
## Agent skills

### Issue tracker

Issues live as GitHub issues on `VictorJohnOkoh/Capstone`, managed via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles, each label string equal to its name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
