// Decides whether a pull request is tied to an accepted change. See .github/workflows/change-link.yml.
import { execFileSync } from "node:child_process";

export const ACCEPTED = "change-control";
export const STANDARD = "standard-change";

const names = (labels) => (labels?.nodes ?? []).map((label) => label.name);

/** Labels can only be set by people with triage access, so a stranger's pull request can't pass by itself. */
export function decide(pr, repository) {
  if (names(pr.labels).includes(STANDARD)) return { ok: true, message: `Labeled ${STANDARD}.` };
  const linked = (pr.closingIssuesReferences?.nodes ?? []).filter((issue) => issue.repository.nameWithOwner === repository);
  const accepted = linked.find((issue) => names(issue.labels).includes(ACCEPTED));
  if (accepted) return { ok: true, message: `Linked to accepted issue #${accepted.number}.` };
  if (linked.length) {
    return { ok: false, message: `Linked to ${linked.map((issue) => `#${issue.number}`).join(", ")}, which a maintainer hasn't accepted yet. Once it has the ${ACCEPTED} label, re-run this check.` };
  }
  return { ok: false, message: 'This pull request needs a linked issue. Add "Closes #123" to its description, using the number of the issue it resolves.' };
}

const QUERY = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      labels(first: 50) { nodes { name } }
      closingIssuesReferences(first: 20) {
        nodes { number repository { nameWithOwner } labels(first: 50) { nodes { name } } }
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
