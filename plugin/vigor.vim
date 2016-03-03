if exists('g:loaded_vigor')
  finish
endif
let g:loaded_vigor = 1

let s:p = fnamemodify(resolve(expand('<sfile>:p')), ':h:h') . '/bin/vigor'
 
function! s:RequireVigor(host) abort
    return rpcstart(s:p, ['plugin'])
endfunction

let s:specs = [
\ {'type': 'autocmd', 'name': 'BufReadCmd', 'sync': 1, 'opts': {'eval': '[expand(''%''), getcwd()]', 'pattern': 'godoc://**'}},
\ {'type': 'command', 'name': 'Fmt', 'sync': 1, 'opts': {'eval': 'expand(''%:p'')', 'range': '%'}},
\ {'type': 'command', 'name': 'Godef', 'sync': 1, 'opts': {'complete': 'customlist,QQQDocComplete', 'eval': 'getcwd()', 'nargs': '*'}},
\ {'type': 'command', 'name': 'Godoc', 'sync': 1, 'opts': {'complete': 'customlist,QQQDocComplete', 'eval': '[getcwd(), 0]', 'nargs': '*'}},
\ {'type': 'command', 'name': 'Pgodoc', 'sync': 1, 'opts': {'complete': 'customlist,QQQDocComplete', 'eval': '[getcwd(), 1]', 'nargs': '*'}},
\ {'type': 'function', 'name': 'QQQDocComplete', 'sync': 1, 'opts': {'eval': 'getcwd()'}},
\ ]

call remote#host#Register('vigor', 'x', function('s:RequireVigor'))
call remote#host#RegisterPlugin('vigor', 'plugin', s:specs)

" vim:ts=4:sw=4:et
