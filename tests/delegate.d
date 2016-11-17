import std.stdio, std.algorithm, std.range;

void main()
{
    int[] a1 = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9];
    int[] a2 = [6, 7, 8, 9];

    // must be immutable to allow access from inside a pure function
    immutable pivot = 5;
 
    int mySum(int a, int b) pure nothrow // pure function
    {
        if (b <= pivot) // ref to enclosing-scope
            return a + b;
        else
            return a;
    }
    
    // passing a delegate (closure)
    auto result = reduce!mySum(chain(a1, a2));
    writeln("Result: ", result); // Result: 15

    // passing a delegate literal
    result = reduce!((a, b) => (b <= pivot) ? a + b : a)(chain(a1, a2));
    writeln("Result: ", result); // Result: 15
}
