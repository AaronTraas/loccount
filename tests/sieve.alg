BEGIN
COMMENT
//////////////////////////////////////////////////////////
// Name: Peter M. Maurer
// Program: Sieve of Eratosthenes
// Due: Never
// Language: Algol 60
// http://cs.ecs.baylor.edu/~maurer/SieveE/SieveE.a60
//////////////////////////////////////////////////////////
;
	COMMENT define the sieve data structure ;
	INTEGER ARRAY candidates[0:1000];
	INTEGER i,j,k;
	COMMENT 1000 to protect against strict evaluation of AND ;
	FOR i := 0 STEP 1 UNTIL 1000 DO
        BEGIN
		COMMENT everything is potentially prime until proven otherwise ;
		candidates[i] := 1;
	END;
	COMMENT Neither 1 nor 0 is prime, so flag them off  ;
	candidates[0] := 0;
	candidates[1] := 0;
	COMMENT start the sieve with the integer 0 ;
	i := 0;
	FOR i := i WHILE i<1000 DO
        BEGIN
		COMMENT advance to the next un-crossed out number. ;
		COMMENT this number must be a prime                 ;
		FOR i := i WHILE i<1000 AND candidates[i] = 0 DO
		BEGIN
			i := i+1;
		END;
		COMMENT insure against running off the end of the data structure ;
		IF i<1000 THEN
		BEGIN
			COMMENT cross out all multiples of the prime, starting with 2*p.;
			j := 2;
			k := j*i;
                        FOR k := k WHILE k < 1000 DO
			BEGIN
				candidates[k] := 0;
                                j := j + 1;
                                k := j*i;
                        END;
			COMMENT advance to the next candidate ;
			i := i+1;
		END
	END;
	COMMENT all uncrossed-out numbers are prime (and only those numbers) ;
	COMMENT print all primes ;
	FOR i := 0 STEP 1 UNTIL 999 DO
	BEGIN
		IF candidates[i] # 0  THEN
		BEGIN
                      write(1,i);
                      text(1," is prime*N")
       		END
	END;
END
FINISH
