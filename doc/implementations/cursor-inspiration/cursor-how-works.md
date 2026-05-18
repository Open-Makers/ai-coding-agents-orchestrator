# How Cursor Works Under the Hood

> Based on the video: *The Real Reason SpaceX is Buying Cursor* (ByteMonk)

---

## The Core Problem

Cursor uses **the same models** everyone else does — GPT, Claude, Gemini. The secret isn't in the model, it's in **how Cursor picks which files from the repository land in the model's context**. Real-world codebases have 50,000+ files, and even the largest models can't fit a whole repo in their context window. Cursor solves this with a clever 6-stage pipeline.

---

## Stage 1 — Indexing (local, on project open)

When you open a project, Cursor immediately starts reading every file **locally on your machine**. First it **filters out the noise**: it ignores `node_modules`, `.git`, build artifacts — only the actual source code moves on for further processing.

---

## Stage 2 — Tree-sitter Parser (semantic code splitting)

Cursor doesn't slice files by line count. It uses the **Tree-sitter** parser, which understands code structure and splits files into semantic units: functions, classes, logical blocks. Each chunk stays intact — cutting a function in half would be useless for search.

---

## Stage 3 — Merkle Tree (efficient change tracking)

After parsing, Cursor builds a **Merkle tree**. Every file gets a fingerprint (hash); every folder gets a hash composed of the hashes of the files inside — all the way up to the root. Thanks to that, on every change Cursor doesn't reindex the whole repo — it compares hashes and processes **only what changed**. That's why Cursor stays fast even on huge codebases.

---

## Stage 4 — Vectors and the database (semantic search)

Cursor converts every code chunk into a **vector** — a list of numbers representing the *meaning* of the code. Authentication code lands in one "neighborhood" of the vector space, payment code lands somewhere completely different, even if they share no words. The vectors go into Cursor's vector database called **Turbopuffer** — effectively "Google for your codebase, but searching by meaning rather than keywords".

> **Privacy:** raw code never leaves the machine. Only encrypted vectors go to the server — file names are obfuscated, chunks encrypted.

---

## Stage 5 — Query Processing (when you type a query)

When you type e.g. *"Refactor the login flow to support Google OAuth"*, Cursor performs three steps:

### 5a. Turning the query into a vector
The question goes through the same embedding model as the code. Now the question and the code live in the same number space and can be compared directly.

### 5b. Nearest Neighbor Search
Cursor looks for which code chunks lie closest to the query vector (top matches). Thanks to that, searching for "login" lands on `authenticate.ts`, which never contained the word "login" — because the *meaning* is the same.

### 5c. Graph traversal (graph expansion)
**This is the part most people miss.** Cursor doesn't stop at the top matches. If `auth_controller.ts` is the best match, Cursor goes further:
- what does this file **import**?
- what **calls** it?
- what does it **call**?

It spreads across the whole network of related code — exactly how a senior developer thinks: not reading files in isolation but tracing the flow.

---

## Stage 6 — Structured Prompt → LLM → Execution Loop

Cursor builds a **structured prompt**:
- the user's question at the top
- the selected code chunks
- their imports and callers
- their roles in the project

The model receives not the whole repo but a precise three-page brief — instead of an entire Confluence wiki.

The model's response comes back as a **diff** — Cursor shows exactly what will change. You click "Apply" and the edit lands in the files.

If something breaks, Cursor reads the error and **retries in a loop**:

```
Diff Generator → Auto Apply → Error Handler → retry with error context → ...
```

Most AI tools stop at "here's the code, paste it yourself" — Cursor doesn't.

---

## Cursor Composer (Cursor 2.0)

In version 2.0 Cursor added its own model called **Composer** — trained specifically for agentic tasks. It doesn't just write code, it uses tools: it searches, edits, runs. Training was done on real codebases using **reinforcement learning**, until the model learned to behave like an engineer who actually delivers working code.

> That's why most Cursor tasks finish **in under 30 seconds**.

---

## TL;DR — The full pipeline

```
Open project
    ↓
Filter noise (no node_modules, .git, builds)
    ↓
Tree-sitter Parser → chunks: functions, classes, blocks
    ↓
Merkle Tree → fingerprints → only delta update on changes
    ↓
Vector embeddings → Turbopuffer (vector DB, locally encrypted)
    ↓
User query → query vector
    ↓
Nearest Neighbor Search → top matches
    ↓
Graph traversal → imports, callers, dependencies
    ↓
Structured Prompt → LLM (Cloud Services: Composer + Code Generation)
    ↓
Diff → Apply
    ↓
Execution Loop: error? → retry with error context
```

**The model is the same as everywhere — the difference lies entirely in what the model sees in its context and how fast it makes it into the code.**
