function copyLink(ev) {
  ev.preventDefault()
  let url = ev.target.parentElement.querySelector('.name_lnk').href

  let ta = document.createElement("textarea")
  document.body.appendChild(ta)
  ta.value = url
  ta.select()
  try {
    document.execCommand("copy")
  } finally {
    document.body.removeChild(ta);
  }
}

function archiveFile(ev) {
  ev.preventDefault()
  let el = ev.target
  let url = el.href
  let lnk = el.parentElement.querySelector('.name_lnk')
  if (lnk._my_deleted) {
    return
  }
  if (lnk._my_archived) {
    console.log('unarchive')
    delete lnk['_my_archived']
    lnk.classList.remove('link_archived')

    fetch(url + "?undo")
  } else {
    console.log('archive')
    lnk._my_archived = 'yes'
    lnk.classList.add('link_archived')

    fetch(url)
  }
}

function deleteFile(ev) {
  ev.preventDefault()
  let el = ev.target
  let url = el.href
  let lnk = el.parentElement.querySelector('.name_lnk')
  if (lnk._my_archived) {
    return
  }
  if (lnk._my_deleted) {
    console.log('undelete')
    delete lnk['_my_deleted']
    lnk.classList.remove('link_deleted')

    fetch(url + "?undo")
  } else {
    console.log('delete')
    lnk._my_deleted = 'yes'
    lnk.classList.add('link_deleted')

    fetch(url)
  }
}
