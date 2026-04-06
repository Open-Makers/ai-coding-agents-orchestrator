# UX/UI Review

Analyze code for user experience quality, accessibility, and usability.

## What to Check

### Error Messages & Feedback
- Error messages are clear and actionable
- Success/failure states are communicated to the user
- Progress indicators for long-running operations
- No raw stack traces or internal errors exposed to users

### Input Handling
- Input validation with helpful, specific error messages
- Sensible defaults for optional parameters
- Consistent input formats across the application

### CLI/API Ergonomics
- Flag/option names are intuitive and consistent
- Help text is complete and includes examples
- Output formatting is readable (tables, JSON, colors)
- Exit codes follow conventions (0=success, 1=error, 2=usage)

### Accessibility
- Keyboard navigation support
- Screen reader compatibility (ARIA labels where applicable)
- Sufficient color contrast
- No color-only information encoding

### Consistency
- Terminology is consistent throughout
- Naming conventions are uniform
- Behavior is predictable across similar operations

### Graceful Degradation
- Fallback behavior when optional features are unavailable
- Meaningful behavior with minimal configuration
- Timeout handling with user-visible feedback

## Response Format

```
MUST FIX
- <critical UX issue>

NICE TO HAVE
- <improvement suggestion>

Approve?: YES or NO
```

