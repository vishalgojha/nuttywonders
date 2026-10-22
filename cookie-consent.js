(function () {
  const key = 'nutty-cookie-consent';
  if (localStorage.getItem(key) === 'accepted') return;
  const banner = document.createElement('aside');
  banner.className = 'cookie-banner';
  banner.setAttribute('role', 'dialog');
  banner.setAttribute('aria-label', 'Cookie and storage notice');
  banner.innerHTML = `<div><strong>Cookies &amp; storage</strong><p>We use essential browser storage to keep your bag and remember this notice. No advertising or analytics cookies are used.</p></div><div class="cookie-banner-actions"><a href="/cookies.html">Learn more</a><button type="button">Okay</button></div>`;
  document.body.appendChild(banner);
  banner.querySelector('button').addEventListener('click', () => {
    localStorage.setItem(key, 'accepted');
    banner.remove();
  });
}());
