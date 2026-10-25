---
name: browser-test
description: Test web applications using real headless browsers. Use when implementing features, fixing bugs, validating UI, testing user journeys, checking responsiveness, or verifying a web product before release.
---

# Browser Testing Skill

## Objective

Use Playwright to verify the running web product through a real browser.
Do not claim a feature works merely because the code looks correct.

## Workflow

1. Inspect the repository, package.json, existing tests, routes, and framework.
2. Identify how the app starts and which environment variables it needs.
3. Reuse existing Playwright configuration and test infrastructure if present.
4. Start the app using the existing development command when necessary.
   Do not start duplicate servers if one is already running.
5. Verify that the application is reachable before testing.
6. Use Playwright with headless Chromium by default.
7. Test the actual rendered interface and user interactions.
8. Check browser console errors, failed requests, and unexpected navigation.
9. Run relevant existing tests and newly created browser tests.
10. Fix reproducible defects within scope and rerun affected tests.
11. Report what was tested, what passed, what failed, and any blockers.

## Test coverage

For relevant application routes, test:

- Page loading and HTTP response status.
- Main headings, navigation, buttons, forms, and important content.
- Links and navigation between routes.
- Form validation, submission, and error states.
- Authentication flows when a safe test account exists.
- CRUD workflows when test data and permissions allow.
- Loading, empty, success, and failure states.
- API failures, relevant network errors, and console errors.
- Responsive layouts at mobile and desktop viewport sizes.
- Screenshots of important pages and failed states.

Adapt tests to the application's actual functionality. Do not invent routes,
credentials, APIs, or expected results.

## Implementation rules

- Prefer accessible locators such as getByRole, getByLabel, and getByText.
- Use stable test IDs when accessible locators are insufficient.
- Avoid arbitrary sleeps. Wait for observable conditions.
- Never use production customer data or perform destructive production actions.
- Do not bypass authentication or security controls.
- Never print secrets, cookies, authorization headers, or tokens.
- Use only explicitly authorized local, staging, or test environments.
- Do not modify application behavior just to make a test pass.
- Keep tests deterministic and independent where possible.
- Preserve existing project conventions and test configuration.

## Browser execution

- Run Chromium in headless mode by default.
- Use existing Playwright Test configuration where available.
- Save screenshots, traces, and reports in a dedicated test-artifacts directory.
- Capture failure screenshots when a test fails.
- Run tests using the repository's existing scripts when available.
- If browser installation, credentials, or environment setup is missing,
  explain the blocker instead of claiming success.

## Completion report

Summarize:

1. Routes and workflows tested.
2. Number of passed, failed, and skipped tests.
3. Bugs found and fixes made.
4. Browser console errors and failed network requests.
5. Artifact locations for screenshots and reports.
6. Remaining risks and tests that could not run.

Never report a test as passed unless it actually executed successfully.
