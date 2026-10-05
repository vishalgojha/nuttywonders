// Keep the Studio static site separate from its server-side API.
(() => {
  const apiOrigin = 'https://api.nuttywonders.com';
  const fetchFromApi = window.fetch.bind(window);
  window.fetch = (resource, options) => resource === '/api/ai/chat'
    ? fetchFromApi(`${apiOrigin}${resource}`, options)
    : fetchFromApi(resource, options);
})();
