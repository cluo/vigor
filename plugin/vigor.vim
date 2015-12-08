if exists('g:loaded_nvimgo')
  finish
endif
let g:loaded_nvimgo = 1

let s:bin = fnamemodify(resolve(expand('<sfile>:p')), ':h') . '/../bin/vigor'

function! s:RequireVigor(host) abort
  let args = []
  let plugins = remote#host#PluginsForHost(a:host.name)
  for plugin in plugins
    call add(args, plugin.path)
  endfor
  return rpcstart(s:bin, args)
endfunction

call remote#host#Register('vigor', '*', function('s:RequireVigor'))


" vim:ts=4:sw=4:et
