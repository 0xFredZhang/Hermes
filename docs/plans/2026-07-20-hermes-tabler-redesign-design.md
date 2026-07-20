# Hermes Tabler Redesign

**Date:** 2026-07-20

## Goal

Replace the current custom Tailwind presentation with the official Tabler admin
framework across the entire Hermes web console while preserving all existing Go
routes, server-rendered workflows, htmx behavior, and operational safeguards.

## Architecture

Install `@tabler/core` and `@tabler/icons-webfont` as local frontend dependencies.
The CSS build will bundle Tabler with a small Hermes theme and compatibility layer
into the existing embedded `/static/app.css`; no CDN or runtime network access is
required. Tabler's JavaScript bundle will be copied into the embedded static assets
only for framework behaviors that the templates use.

The shared layout becomes a responsive Tabler application shell: a compact desktop
sidebar, a mobile navigation collapse, a restrained top bar, and a consistent page
header. Accounts, projects, blueprints, and environments remain the four primary
navigation destinations. Login uses the same design vocabulary without application
navigation.

Existing element IDs, form actions, methods, `hx-*` attributes, ARIA relationships,
and JavaScript hooks remain stable. Template changes are visual and structural;
API handlers, orchestration, persistence, and Pulumi behavior are outside scope.

## Component System

- Lists use Tabler tables, responsive wrappers, deliberate empty states, status
  badges, and compact action groups.
- Create, edit, deploy, and delete flows use consistent form controls, field groups,
  validation feedback, and explicit danger zones.
- Detail views use unframed page sections or single-purpose cards for metadata,
  outputs, summaries, and logs. Cards are not nested.
- Buttons use Tabler hierarchy and Tabler icons where the command has a familiar
  symbol. Text remains where the command could otherwise be ambiguous.
- Status colors remain semantic. Hermes keeps its restrained steel-blue identity;
  red, amber, and green are reserved for real operational states.
- The native confirmation dialog is restyled to match Tabler while retaining its
  current JavaScript behavior and accessible labels.

## Responsive Behavior

The sidebar collapses below the desktop breakpoint. Page actions wrap without
overlapping headings. Tables retain their current small-screen label behavior so
dense resource data remains readable rather than becoming a clipped horizontal
canvas. Long IDs, stack names, errors, outputs, and logs wrap or scroll within
stable bounds.

## Feedback And Errors

Current server and htmx error paths remain authoritative. Notice regions map to
Tabler alerts without changing IDs or live-region semantics. Loading labels,
disabled controls, field errors, job-state badges, destructive confirmations, and
live logs keep their behavior and gain consistent visual states. Motion is limited
to short state transitions and respects `prefers-reduced-motion`.

## Verification

Add template contract tests before changing production templates. The tests will
assert that Tabler assets and shell markers are present while the existing hooks
remain intact. Run the focused Go web tests, JavaScript tests, full Go test suite,
and production CSS build. Finally, start the local server and verify representative
list, form, detail, destructive, login, desktop, and mobile states in the browser,
including console errors and layout overflow.

