## Preparation

Check the PR for the current branch.

```
gh pr view --json number,url,headRefName
```

If no PR exists, stop. Note the owner, repo, and PR number for use in subsequent steps.

## Fetch Unresolved Comments

Use GraphQL to get a list of unresolved review threads.

```
gh api graphql -F owner=OWNER -F repo=REPO -F number=NUMBER -f query='
  query($owner:String!, $repo:String!, $number:Int!) {
    repository(owner:$owner, name:$repo) {
      pullRequest(number:$number) {
        reviewThreads(first:100) {
          nodes {
            id
            isResolved
            comments(first:10) {
              nodes {
                id
                databaseId
                body
                path
                line
                author { login }
                url
              }
            }
          }
        }
      }
    }
  }
'
```

Only process threads where `isResolved: false`. If there are none, stop.

## Address Each Unresolved Thread

Process each thread in order as follows.

### Step 1: Assess validity of the comment

Before making any change, evaluate whether the suggested fix is actually correct and beneficial for this codebase. Consider:

- Does the fix align with the project's design principles, conventions, and goals?
- Could the suggestion be based on a misunderstanding of the context (e.g., applying general style rules to a domain-specific file like an AI prompt)?
- Does it improve correctness, clarity, or maintainability — or is it a stylistic preference that doesn't apply here?

Based on this assessment, classify the thread as one of:
- **Valid**: The fix is clearly correct and beneficial.
- **Invalid**: The fix is incorrect or inappropriate for this context.
- **Unclear**: You are uncertain whether the fix is appropriate.

### When the comment is valid and the fix is clear

1. Fix the code as indicated by the comment.
2. Run `make lint` and `make test` to confirm no errors.
3. Commit.
4. Reply to the PR comment thread with a description of the fix (in English).

   ```
   gh api repos/OWNER/REPO/pulls/NUMBER/comments/COMMENT_ID/replies \
     -X POST -f body="Description of the fix in English"
   ```

5. Resolve the thread.

   ```
   gh api graphql -F threadId=THREAD_ID -f query='
     mutation($threadId:ID!) {
       resolveReviewThread(input:{threadId:$threadId}) {
         thread { id isResolved }
       }
     }
   '
   ```

### When the comment is invalid

1. Reply to the PR comment thread explaining why the suggestion does not apply (in English).

   ```
   gh api repos/OWNER/REPO/pulls/NUMBER/comments/COMMENT_ID/replies \
     -X POST -f body="Explanation of why the suggestion is not applicable"
   ```

2. Resolve the thread.

   ```
   gh api graphql -F threadId=THREAD_ID -f query='
     mutation($threadId:ID!) {
       resolveReviewThread(input:{threadId:$threadId}) {
         thread { id isResolved }
       }
     }
   '
   ```

### When the fix is unclear

Skip and move to the next thread (revisit in a later step).

## Push

Once all clear comments have been addressed, run `git push`.

## Revisit Skipped Threads

For each skipped thread, present the following:

- **Problem summary**: Briefly describe the issue raised by the comment.
- **Proposed approaches**: List multiple possible options with pros and cons for each.
- **Recommendation**: If possible, recommend one option and explain why.
