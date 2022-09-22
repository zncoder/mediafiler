async function copyLink(ev) {
  ev.preventDefault()
  let a = ev.target.parentElement.querySelector('a')
  await navigator.clipboard.writeText(a.href)
  console.log(a.href)
}
