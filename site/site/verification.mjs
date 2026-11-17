// Public search-engine verification tokens. They are emitted into the HTML,
// so they are public by design. Never put an API key or a secret here.
//
// Prefer the environment variables in CI or the Pages build settings. These
// values are the fallback when the token is committed explicitly.
export const verification = {
  google: '',
  bing: '',
};
