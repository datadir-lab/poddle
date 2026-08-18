// Conventional Commits for the poddle core, matching the existing history
// (e.g. "feat(web): ...", "fix(up): ...", "docs(legal): ...").
// https://www.conventionalcommits.org
module.exports = {
  extends: ['@commitlint/config-conventional'],
  rules: {
    // Detailed bodies and footer URLs (issue links, trailers) are welcome here.
    'body-max-line-length': [0, 'always'],
    'footer-max-line-length': [0, 'always'],
  },
};
