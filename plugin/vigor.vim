if exists('g:loaded_nvimgo')
  finish
endif
let g:loaded_nvimgo = 1

let s:p = fnamemodify(resolve(expand('<sfile>:p')), ':h:h') . '/bin/vigor'
 
function! s:RequireVigor(host) abort
    let args = []
    for plugin in remote#host#PluginsForHost(a:host.name)
        call add(args, plugin.path)
    endfor
    return rpcstart(s:p, args)
endfunction

call remote#host#Register('vigor', '*', function('s:RequireVigor'))

" vim:ts=4:sw=4:et
