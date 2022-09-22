async function copyLink(ev) {
  ev.preventDefault()
  let url = ev.target.parentElement.querySelector('a').href

  let ta = document.createElement("textarea")
  ta.textContent = url
  ta.style.position = "fixed"
  document.body.appendChild(ta)
  ta.select()
  try {
    document.execCommand("copy")
  } finally {
    document.body.removeChild(ta);
  }
}
