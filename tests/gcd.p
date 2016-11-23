;;; Computing GCD in POP-11 -- iterative version

define my_gcd(k, l) -> r;
  lvars k , l, r = l;
  abs(k) -> k;
  abs(l) -> l;
  if k < l then (k, l) -> (l, k) endif;
  while l /= 0 do
    (l, k rem l) -> (k, l)
  endwhile;
  k -> r;
enddefine;
