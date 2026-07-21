try {
  document.documentElement.removeAttribute('data-theme');
  var theme = localStorage.getItem('share-theme');
  if (theme === 'light' || theme === 'dark') {
    document.documentElement.setAttribute('data-theme', theme);
  }
} catch {}
