// Decides whether a pull request has a reason on record, and says what AI was used. See .github/workflows/change-link.yml.
import { execFileSync } from "node:child_process";

export const STANDARD = "standard-change";

const names = (labels) => (labels?.nodes ?? []).map((label) => label.name);

/** What the description's "AI used" section says, or null if it's missing or empty. */
export function aiUsed(body) {
  const section = /^#{1,6}\s+AI used\s*$([\s\S]*?)(?=^#{1,6}\s|(?![\s\S]))/im.exec(String(body ?? "").replace(/\r\n/g, "\n"));
  const text = section?.[1].replace(/<!--[\s\S]*?-->/g, "").trim();
  return text || null;
}

/** A change needs a reason on record. Whether it's accepted is decided by the person who merges it. */
export function decide(pr, repository) {
  // Labels can only be set by people with triage access, so a stranger's pull request can't use this path.
  if (names(pr.labels).includes(STANDARD)) return { ok: true, message: `Labeled ${STANDARD}.` };
  const linked = (pr.closingIssuesReferences?.nodes ?? []).filter((issue) => issue.repository.nameWithOwner === repository);
  if (!linked.length) return { ok: false, message: 'This pull request needs a linked issue. Add "Closes #123" to its description, using the number of the issue it resolves.' };
  // Agents usually open pull requests with their own description, which skips the template, so this is asked here too.
  if (!aiUsed(pr.body)) return { ok: false, message: 'This pull request needs an "AI used" section. Add "## AI used" to its description, then list the AI tools that assisted, or write "None".' };
  return { ok: true, message: `Linked to issue #${linked[0].number}.` };
}

const QUERY = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      body
      labels(first: 50) { nodes { name } }
      closingIssuesReferences(first: 20) {
        nodes { number repository { nameWithOwner } }
      }
    }
  }
}`;

if (process.argv[1] === new URL(import.meta.url).pathname) {
  const repository = process.env.REPOSITORY;
  const [owner, name] = repository.split("/");
  const output = execFileSync("gh", ["api", "graphql", "-f", `query=${QUERY}`, "-f", `owner=${owner}`, "-f", `name=${name}`, "-F", `number=${process.env.PR_NUMBER}`], { encoding: "utf8" });
  const result = decide(JSON.parse(output).data.repository.pullRequest, repository);
  console.log(result.ok ? result.message : `::error::${result.message}`);
  process.exit(result.ok ? 0 : 1);
}
