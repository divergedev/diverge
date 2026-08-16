(function() {
  if (document.querySelector('#diverge-preview-banner')) return;
  var banner = document.createElement('div');
  banner.id = 'diverge-preview-banner';
  banner.style.cssText = 'position:fixed;{{POSITION}}:0;left:0;right:0;z-index:999999;background:{{COLOR}};color:white;text-align:center;padding:4px 8px;font-family:system-ui;font-size:12px;font-weight:600;opacity:0.9;';
  banner.textContent = '{{TEXT}} — {{BRANCH}}';
  var close = document.createElement('span');
  close.textContent = '×';
  close.style.cssText = 'cursor:pointer;margin-left:12px;font-size:16px;';
  close.onclick = function() { banner.remove(); };
  banner.appendChild(close);
  document.body.prepend(banner);
})();
