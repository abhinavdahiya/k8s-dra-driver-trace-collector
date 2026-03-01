---
description: Conventions for diagrams in specs
globs: ["specs/**"]
---

# Diagram Conventions

- Use **Mermaid** for all diagrams in specs.
- Store Mermaid source files in `specs/diagrams/` (not in `specs/` directly). Use `.mmd` extension.
- Use `mmdc` CLI to render PNGs: `mmdc -i specs/diagrams/<name>.mmd -o specs/images/<name>.png -b white`
- Embed the PNG in the spec markdown and link back to the source `.mmd` file:
  ```markdown
  ![Diagram Title](images/<name>.png) ([source](diagrams/<name>.mmd))
  ```
- Keep the Mermaid source in a fenced ```` ```mermaid ```` block in the spec as well (for GitHub rendering), immediately after the image embed.
