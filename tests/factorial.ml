(* should count as 8 lines *)
fun factorial 0 = 1
| factorial n = n * factorial (n-1);

(* the factorial function, defined by giving two cases *)

fun fib 1 = 1
| fib 2 = 1
| fib n = fib(n-1) + fib(n-2);

(* fib n is the nth Fibonacci number *)

fun iota 0 = []
| iota n = 1::(map increment (iota (n-1)))
and increment n = n+1;

(* iota n is just the list [1, 2, 3, ... n]. increment is the function that adds 1 to its argument. map f l is the (built-in) function that applies its first argument f to its second argument (a list), and returns a list of the results. *)
