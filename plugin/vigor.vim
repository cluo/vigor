if exists('g:loaded_nvimgo')
  finish
endif
let g:loaded_nvimgo = 1


function! s:RequireVigor(host) abort
    let base = fnamemodify(resolve(expand('<sfile>:p')), ':h:h:h')
    let args = []
    for plugin in remote#host#PluginsForHost(a:host.name)
        call add(args, plugin.path)
    endfor
    return rpcstart(base . '/bin/vigor', args)
endfunction

call remote#host#Register('vigor', '*', function('s:RequireVigor'))

" vim:ts=4:sw=4:et
