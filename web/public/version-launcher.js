/**
 * version-launcher.js — 通用 UI 版本跳转与切换工具。
 *
 * 约定：
 * - Default / Latest 版本路径为根路径 "/"。
 * - 历史版本路径为 "/ui/v{N}/"。
 * - localStorage key "map_ui_version" 保存偏好："latest" | "v1" | "v2" ...
 */
(function () {
  var STORAGE_KEY = 'map_ui_version';

  // 当前页面所属版本：根路径为 latest，否则从 /ui/vN/ 解析
  function getCurrentVersion() {
    var path = window.location.pathname;
    var m = /^\/ui\/(v\d+)\//.exec(path);
    return m ? m[1] : 'latest';
  }

  function getPreferredVersion() {
    try {
      return localStorage.getItem(STORAGE_KEY) || 'latest';
    } catch (e) {
      return 'latest';
    }
  }

  function pathForVersion(version) {
    if (version === 'latest') return '/';
    return '/ui/' + version + '/';
  }

  function redirectTo(version) {
    var target = pathForVersion(version) + window.location.search + window.location.hash;
    if (window.location.pathname + window.location.search + window.location.hash !== target) {
      window.location.replace(target);
    }
  }

  // 页面加载时：如果路径版本与偏好不一致，立即跳转
  var current = getCurrentVersion();
  var preferred = getPreferredVersion();
  if (current !== preferred) {
    redirectTo(preferred);
  }

  // 暴露给各版本 UI 的全局切换函数
  window.switchUIVersion = function (version) {
    try {
      localStorage.setItem(STORAGE_KEY, version);
    } catch (e) {
      // ignore storage errors
    }
    redirectTo(version);
  };
})();
