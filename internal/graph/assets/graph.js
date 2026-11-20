(() => {
  "use strict";
  const el = document.getElementById("graphData");
  if (el && el.textContent) JSON.parse(el.textContent);
})();
