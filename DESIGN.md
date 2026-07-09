---
name: Hermes
description: Calm self-hosted AWS provisioning console for small teams.
colors:
  console-canvas: "#f6f8fb"
  console-surface: "#ffffff"
  console-subtle: "#eef2f6"
  console-border: "#d8dee7"
  console-ink: "#111827"
  console-muted: "#4b5563"
  console-primary: "#255f85"
  console-primary-strong: "#174565"
  console-danger: "#b42318"
  console-danger-soft: "#fef3f2"
  console-success: "#087443"
  console-success-soft: "#ecfdf3"
  console-log: "#101828"
  console-log-text: "#e5e7eb"
typography:
  display:
    fontFamily: "system-ui, sans-serif"
    fontSize: "2rem"
    fontWeight: 700
    lineHeight: 1.2
    letterSpacing: "0"
  headline:
    fontFamily: "system-ui, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 700
    lineHeight: 1.25
    letterSpacing: "0"
  title:
    fontFamily: "system-ui, sans-serif"
    fontSize: "1.17rem"
    fontWeight: 700
    lineHeight: 1.3
    letterSpacing: "0"
  body:
    fontFamily: "system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: "normal"
    letterSpacing: "0"
  label:
    fontFamily: "system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: "normal"
    letterSpacing: "0"
rounded:
  sm: "6px"
spacing:
  xs: "4.8px"
  sm: "6.4px"
  md: "8px"
  lg: "12px"
  xl: "16px"
  page-y: "32px"
components:
  button-default:
    backgroundColor: "{colors.console-surface}"
    textColor: "{colors.console-ink}"
    typography: "{typography.body}"
    padding: "{spacing.sm}"
    rounded: "{rounded.sm}"
  button-primary:
    backgroundColor: "{colors.console-primary}"
    textColor: "{colors.console-surface}"
    typography: "{typography.body}"
    padding: "{spacing.sm}"
    rounded: "{rounded.sm}"
  input-default:
    backgroundColor: "{colors.console-surface}"
    textColor: "{colors.console-ink}"
    typography: "{typography.body}"
    padding: "{spacing.sm}"
    rounded: "{rounded.sm}"
  table-cell:
    textColor: "{colors.console-ink}"
    typography: "{typography.body}"
    padding: "{spacing.md}"
  log-panel:
    backgroundColor: "{colors.console-log}"
    textColor: "{colors.console-log-text}"
    rounded: "{rounded.sm}"
    padding: "{spacing.lg}"
---

# Design System: Hermes

## 1. Overview

**Creative North Star: "The Calm Control Room"**

Hermes should feel like a compact operations console for real AWS resources: quiet, legible, and direct. The implementation remains deliberately lightweight - Go templates, htmx, server-rendered tables and forms, and a Tailwind-compiled stylesheet - so the design system strengthens the product shape without replacing it with a heavy application framework.

The visual direction is restrained product UI. It preserves fast scanning for accounts, projects, blueprints, environments, previews, and logs. Tailwind provides consistent primitives, clearer hierarchy, focus states, and safer destructive-action affordances while keeping the page simple enough to remain a self-hosted tool.

This system rejects the PRODUCT.md anti-references by name: marketing-style SaaS pages, oversized hero sections, decorative gradients, playful illustrations, ornamental dashboards, terminal-only aesthetics, AWS-console-level density, and anything that looks experimental while creating or destroying cloud resources.

**Key Characteristics:**
- Compact server-rendered workflows with readable forms and tables.
- Restrained neutrals with semantic status colors reserved for real state.
- System typography only; no display fonts, no decorative type pairing.
- Flat by default, with borders and tonal contrast doing the work.
- Explicit action hierarchy for preview, provision, retry, drift detection, and destroy.

## 2. Colors

Hermes uses a restrained Tailwind palette: a cool off-white app canvas, white work panels, low-contrast dividers, one steel-blue primary accent, and semantic red/green states. The palette should stay operational rather than decorative.

### Primary
- **Console Primary**: A restrained steel blue used for primary buttons, focus treatment, links, and future active navigation.
- **Console Primary Strong**: The hover and active state for primary controls.

### Neutral
- **Console Canvas**: The app background is a cool near-white. Keep the main work surface light and quiet.
- **Console Surface**: Panels, tables, and grouped controls sit on white.
- **Console Subtle**: Header rows, status strips, and hover fills use a low-contrast neutral layer.
- **Console Border**: Borders are the main depth mechanism for tables, inputs, panels, and field groups.
- **Console Ink**: Primary text for body copy, table values, labels, and headings.
- **Console Muted**: Secondary stack names, empty states, metadata, and operational notes.
- **Console Log**: The log panel uses a dark navy surface with light text. Keep logs visually distinct from forms and tables.

### Semantic
- **Danger**: Error messages, failed states, delete, destroy, and confirm-destroy actions.
- **Danger Soft**: Background tint for error notices.
- **Success**: Ready states and successful preview summaries.
- **Success Soft**: Background tint for successful notices.

### Named Rules
**The Semantic Scarcity Rule.** Red and green are reserved for actual state, never for decoration or brand flourish.

**The One Accent Future Rule.** When Tailwind is introduced, add one restrained primary accent for primary actions and active navigation; keep it under 10% of any screen.

## 3. Typography

**Display Font:** `system-ui, sans-serif`
**Body Font:** `system-ui, sans-serif`
**Label/Mono Font:** none yet; logs currently inherit the same font stack.

**Character:** System typography is correct for Hermes. It keeps the console familiar, fast, and native-feeling. The next pass should tighten hierarchy with explicit sizes and weights instead of introducing decorative fonts.

### Hierarchy
- **Display** (700, 2rem, 1.2): Product title only. Avoid hero-scale display text in this tool.
- **Headline** (700, 1.5rem, 1.25): Page titles such as Accounts, Projects, Blueprints, and Environments.
- **Title** (700, 1.17rem, 1.3): Section labels such as live logs and grouped outputs.
- **Body** (400, 1rem, normal): Tables, form values, paragraphs, and operational copy.
- **Label** (400, 1rem, normal): Current forms place labels inline with controls; future forms should give labels consistent block rhythm.

### Named Rules
**The Native Tool Rule.** Use one system font stack across UI labels, tables, buttons, and forms. No display font belongs in the console.

**The Fixed Scale Rule.** Product UI type sizes are fixed rem values, not viewport-scaled hero type.

## 4. Elevation

Hermes is flat today. There are no shadows in the current templates; depth comes from table borders, whitespace, fieldsets, and the dark log panel. Keep this direction. If shadows are added later, they should be structural and subtle, never decorative.

### Named Rules
**The Flat-By-Default Rule.** Surfaces are flat at rest. Use borders, spacing, and tonal layers before shadows.

**The Log Contrast Rule.** The log panel may use a strong dark surface because it is a distinct stream of machine output; do not apply that dark treatment broadly to the whole app.

## 5. Components

Hermes has Tailwind-backed semantic HTML primitives rather than a JavaScript component library. The component system stabilizes these primitives first: top navigation, buttons, inputs/selects, fieldsets, tables, status text, empty states, and logs.

### Buttons
- **Shape:** Gently squared controls with a small radius (6px).
- **Primary:** Primary actions use Console Primary with white text, especially "Add and verify", "Save blueprint", "Deploy", and "Confirm create".
- **Hover / Focus:** Buttons have hover, active, disabled, and visible `:focus-visible` states.
- **Danger:** Delete, destroy, and confirm-destroy actions must not look identical to safe actions.

### Inputs / Fields
- **Style:** Inputs and selects share compact padding, border vocabulary, white surface, and 6px radius.
- **Focus:** Fields use a primary border and soft primary ring.
- **Search:** Blueprint search fields should remain lightweight and close to their paired selects.
- **Disabled:** Disabled controls use opacity and `cursor: not-allowed`; keep the pattern but pair it with accessible contrast.

### Tables
- **Style:** Tables fill the page width with collapsed borders and compact cell padding.
- **Header:** Headers should be visually distinct enough to scan long account, project, blueprint, and environment lists.
- **Rows:** Empty rows should teach the next action, not only say that nothing exists.
- **Actions:** Row actions should be right-aligned and clearly grouped.

### Navigation
- **Style:** Current navigation is a simple horizontal list with a floating logout button.
- **Active State:** Future navigation needs an active route treatment so users know whether they are managing accounts, projects, blueprints, or environments.
- **Mobile:** Collapse structurally if needed; do not rely on shrinking type.

### Status Text
- **Success:** Use the success color only for ready, preview-ready, or confirmed safe states.
- **Danger:** Use danger only for failures and destructive confirmations.
- **Muted:** Use muted text for destroyed, refreshing, empty, and secondary stack metadata.
- **Color Independence:** Pair color with text labels; status cannot rely on color alone.

### Log Panel
- **Style:** The log panel is the strongest existing visual component: dark background, light text, small radius, constrained height, and overflow scrolling.
- **Behavior:** Keep auto-scroll for live logs, but avoid animations. Logs are an operational readout, not a performance.

## 6. Do's and Don'ts

### Do:
- **Do** preserve the self-hosted, lightweight feel: Go templates, htmx flows, and server-rendered forms should remain first-class.
- **Do** use restrained neutral surfaces, clear dividers, and one future primary accent for primary actions and active navigation.
- **Do** make risky actions explicit: preview, confirm create, destroy preview, confirm destroy, retry, and drift detection need distinct copy and visual hierarchy.
- **Do** give every interactive primitive hover, active, disabled, and visible focus states.
- **Do** keep empty states useful: point users toward adding an account, creating a project, saving a blueprint, or deploying an environment.
- **Do** keep log output visually distinct with the dark log surface and a readable text color.

### Don't:
- **Don't** use marketing-style SaaS pages, oversized hero sections, decorative gradients, playful illustrations, or ornamental dashboards.
- **Don't** use terminal-only aesthetics that make normal forms and tables feel hostile.
- **Don't** copy AWS-console-level density where common actions become hard to scan.
- **Don't** make destructive actions look identical to safe actions.
- **Don't** rely on red or green for decoration; they are semantic state colors only.
- **Don't** introduce custom scrollbars, unusual form controls, or modal-heavy flows for flavor.
