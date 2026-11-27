# mf

`mf` aggregates separately built CLI tools under a single command, so `mf <name>` runs the **Plugin** called `<name>`.

## Language

**Plugin**:
An executable named `mf-<name>` that `mf` runs as `mf <name>`.
_Avoid_: extension, subcommand (reserved for built-in commands)

**Managed plugin**:
A **Plugin** installed by `mf plugin install` into the **Plugin registry**, as opposed to one found elsewhere on the user's `PATH`.
_Avoid_: installed plugin (ambiguous: PATH plugins are installed too)

**Plugin registry**:
The record of **Managed plugins** together with the **Search path** that decides which **Plugin** a name resolves to.
_Avoid_: plugin index, plugin store

**Search path**:
The ordered list of directories looked through for **Plugins**: the **Plugin registry**'s own directory first, then the user's `PATH`. The first match for a name wins.
_Avoid_: lookup path, plugin path

**Plugin environment**:
The environment variables a **Plugin** receives when `mf` runs it.
_Avoid_: plugin env contract, env allowlist
