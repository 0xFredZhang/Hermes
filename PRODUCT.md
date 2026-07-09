# Product

## Register

product

## Users

Hermes is for a single operator or small engineering team managing AWS development infrastructure from a self-hosted console. Users are usually in an operations or development workflow: adding cloud accounts, defining reusable environment blueprints, previewing infrastructure changes, watching provisioning logs, detecting drift, and safely destroying resources after use.

## Product Purpose

Hermes turns repeated manual AWS console work into a controlled provisioning workflow. It centralizes encrypted AWS account credentials, project and blueprint definitions, Pulumi previews, asynchronous job execution, live logs, environment outputs, drift checks, and destroy previews. Success means users can create and clean up EC2, networking, optional RDS, and optional Redis resources with less repetition, fewer manual mistakes, and clear visibility into what will change before cloud resources are touched.

## Brand Personality

Calm, reliable, and operational. The interface should feel like a capable internal control room: clear enough for repeated daily use, restrained enough to keep risky cloud actions legible, and confident without behaving like a marketing product.

## Anti-references

Avoid marketing-style SaaS pages, oversized hero sections, decorative gradients, playful illustrations, and ornamental dashboards that obscure the task. Avoid terminal-only aesthetics that make normal forms and tables feel hostile. Avoid AWS-console-level density where common actions become hard to scan. The product should not look experimental when it is asking users to create or destroy real cloud resources.

## Design Principles

- Make risky actions explicit: previews, current status, target environment, and destructive controls should be visually unmistakable.
- Keep repeated operations fast: forms, tables, navigation, and logs should support scanning and quick correction without extra ceremony.
- Preserve operational trust: surface errors, pending work, outputs, and state transitions clearly instead of hiding them behind generic messages.
- Stay self-hosted and lightweight: prefer simple, robust UI patterns that fit Go templates, htmx, and server-rendered flows.
- Teach through context: empty states and inline hints should help users understand the next operational step without turning the UI into documentation.

## Accessibility & Inclusion

Target WCAG AA best-effort for the management console. Text and placeholders should maintain readable contrast, controls should expose visible focus states, forms should use semantic labels where practical, status should not rely on color alone, and motion should be minimal with reduced-motion behavior respected.
