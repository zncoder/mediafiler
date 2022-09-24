function copyLink(ev) {
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

function deleteFile(ev) {
  ev.preventDefault()
  let el = ev.target
  let url = el.href
  let lnk = el.parentElement.querySelector('a')
  if (lnk.deleted) {
    console.log('undelete')
    delete lnk['deleted']
    lnk.style.removeProperty('text-decoration')
    lnk.style.removeProperty('background-color')

    fetch(url + "?undelete")
  } else {
    console.log('delete')
    lnk.deleted = 'yes'
    lnk.style.textDecoration = 'line-through'
    lnk.style.backgroundColor = 'DimGray'

    fetch(url)
  }
}
