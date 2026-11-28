// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The math package's functions, each the system's math library's, and
// the predeclared min and max of floats.

#include "internal.h"

#include <float.h>
#include <math.h>

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

// sn_f64_min and sn_f64_max are the predeclared min and max on floats:
// NaN when either is NaN, and -0 below +0.
double sn_f64_min(double a, double b) {
    if (a != a || b != b) {
        return a != a ? a : b;
    }

    if (a == b) {
        return signbit(a) ? a : b;
    }

    return a < b ? a : b;
}

double sn_f64_max(double a, double b) {
    if (a != a || b != b) {
        return a != a ? a : b;
    }

    if (a == b) {
        return signbit(a) ? b : a;
    }

    return a > b ? a : b;
}

// The functions of package math, each one call of the math library's.
// A build that optimizes the program and the runtime as one unit
// writes the library's in its place, and the compiler knows the square
// root and the four roundings as instructions where the processor has
// them. Inf, NaN and the three tests are written here: they are a
// constant or a comparison.
double sn_math_Sqrt(double x) { return sqrt(x); }
double sn_math_Cbrt(double x) { return cbrt(x); }
double sn_math_Floor(double x) { return floor(x); }
double sn_math_Ceil(double x) { return ceil(x); }
double sn_math_Trunc(double x) { return trunc(x); }
double sn_math_Round(double x) { return round(x); }
double sn_math_Mod(double x, double y) { return fmod(x, y); }
double sn_math_Pow(double x, double y) { return pow(x, y); }
double sn_math_Exp(double x) { return exp(x); }
double sn_math_Log(double x) { return log(x); }
double sn_math_Log2(double x) { return log2(x); }
double sn_math_Log10(double x) { return log10(x); }
double sn_math_Sin(double x) { return sin(x); }
double sn_math_Cos(double x) { return cos(x); }
double sn_math_Tan(double x) { return tan(x); }
double sn_math_Asin(double x) { return asin(x); }
double sn_math_Acos(double x) { return acos(x); }
double sn_math_Atan(double x) { return atan(x); }
double sn_math_Atan2(double y, double x) { return atan2(y, x); }
double sn_math_Sinh(double x) { return sinh(x); }
double sn_math_Cosh(double x) { return cosh(x); }
double sn_math_Tanh(double x) { return tanh(x); }
double sn_math_Hypot(double p, double q) { return hypot(p, q); }
double sn_math_Inf(int64_t sign) { return sign >= 0 ? INFINITY : -INFINITY; }
double sn_math_NaN(void) { return NAN; }
bool sn_math_IsNaN(double x) { return x != x; }
bool sn_math_IsInf(double x, int64_t sign) { return (sign >= 0 && x > DBL_MAX) || (sign <= 0 && x < -DBL_MAX); }
bool sn_math_Signbit(double x) { return signbit(x) != 0; }
double sn_math_Copysign(double x, double sign) { return copysign(x, sign); }
double sn_math_Remainder(double x, double y) { return remainder(x, y); }
double sn_math_FMA(double x, double y, double z) { return fma(x, y, z); }
double sn_math_Expm1(double x) { return expm1(x); }
double sn_math_Log1p(double x) { return log1p(x); }
double sn_math_Exp2(double x) { return exp2(x); }
double sn_math_Acosh(double x) { return acosh(x); }
double sn_math_Nextafter(double x, double y) { return nextafter(x, y); }

// sn_math_Ldexp scales frac by 2^exp: the library's ldexp takes an int,
// so an exponent past what any double's scaling needs is held to a
// bound that overflows or underflows the same way.
double sn_math_Ldexp(double frac, int64_t exp) {
    enum { BOUND = 4096 }; // past twice a double's exponent range either way
    int e = exp > BOUND ? BOUND : exp < -BOUND ? -BOUND : (int)exp;
    return ldexp(frac, e);
}

// sn_math_frexp breaks x into a fraction in [0.5, 1) and a power of two,
// the fraction returned and the power stored: zero for a zero, an
// infinity or NaN, which come back as they are.
double sn_math_frexp(double x, int64_t *exp) {
    if (x == 0 || x != x || x > DBL_MAX || x < -DBL_MAX) {
        *exp = 0;
        return x;
    }

    int e;
    double frac = frexp(x, &e);
    *exp = e;
    return frac;
}

// sn_math_modf breaks x into its whole part, returned, and its
// fraction, stored, both with the sign of x; an infinity's fraction is
// NaN, as the difference of two infinities is.
double sn_math_modf(double x, double *frac) {
    if (x > DBL_MAX || x < -DBL_MAX) {
        *frac = NAN;
        return x;
    }

    double whole;
    *frac = modf(x, &whole);
    return whole;
}


// Six functions the system's library computes slower than the reference
// algorithms on the machines measured are written here from them: the
// error functions, the inverse hyperbolic sine and tangent and the
// logarithm of the gamma function from FreeBSD's msun, and the gamma
// function from Cephes. Each holds the same special cases as the
// library's and is within a few units in the last place of the true
// value.

// erf_tail is the part the error function and its complement share for
// 0.84375 <= x < 6 and up to 28: exp(-x*x) times a rational function
// of 1/x^2, the exponent split so that the square of x rounded to 21
// significant bits is exact.
static double erf_tail(double x) {
    static const double ra[8] = { -9.86494403484714822705e-03, -6.93858572707181764372e-01, -1.05586262253232909814e+01,
                                  -6.23753324503260060396e+01, -1.62396669462573470355e+02, -1.84605092906711035994e+02,
                                  -8.12874355063065934246e+01, -9.81432934416914548592e+00 };
    static const double sa[9] = { 0, 1.96512716674392571292e+01, 1.37657754143519042600e+02, 4.34565877475229228821e+02,
                                  6.45387271733267880336e+02, 4.29008140027567833386e+02, 1.08635005541779435134e+02,
                                  6.57024977031928170135e+00, -6.04244152148580987438e-02 };
    static const double rb[7] = { -9.86494292470009928597e-03, -7.99283237680523006574e-01, -1.77579549177547519889e+01,
                                  -1.60636384855821916062e+02, -6.37566443368389627722e+02, -1.02509513161107724954e+03,
                                  -4.83519191608651397019e+02 };
    static const double sb[8] = { 0, 3.03380607434824582924e+01, 3.25792512996573918826e+02, 1.53672958608443695994e+03,
                                  3.19985821950859553908e+03, 2.55305040643316442583e+03, 4.74528541206955367215e+02,
                                  -2.24409524465858183362e+01 };
    double s = 1 / (x * x), R, S;
    if (x < 1 / 0.35) {
        R = ra[0] + s * (ra[1] + s * (ra[2] + s * (ra[3] + s * (ra[4] + s * (ra[5] + s * (ra[6] + s * ra[7]))))));
        S = 1 + s * (sa[1] + s * (sa[2] + s * (sa[3] + s * (sa[4] + s * (sa[5] + s * (sa[6] + s * (sa[7] + s * sa[8])))))));
    } else {
        R = rb[0] + s * (rb[1] + s * (rb[2] + s * (rb[3] + s * (rb[4] + s * (rb[5] + s * rb[6])))));
        S = 1 + s * (sb[1] + s * (sb[2] + s * (sb[3] + s * (sb[4] + s * (sb[5] + s * (sb[6] + s * sb[7]))))));
    }

    double z = sn_f64_from_bits(sn_f64_bits(x) & 0xffffffff00000000u);
    return exp(-z * z - 0.5625) * exp((z - x) * (z + x) + R / S) / x;
}

// erf_mid is the error function less erx, 0.845..., for 0.84375 <= x <
// 1.25, a rational function of x - 1.
static double erf_mid(double x) {
    static const double pa[7] = { -2.36211856075265944077e-03, 4.14856118683748331666e-01, -3.72207876035701323847e-01,
                                  3.18346619901161753674e-01, -1.10894694282396677476e-01, 3.54783043256182359371e-02,
                                  -2.16637559486879084300e-03 };
    static const double qa[7] = { 0, 1.06420880400844228286e-01, 5.40397917702171048937e-01, 7.18286544141962662868e-02,
                                  1.26171219808761642112e-01, 1.36370839120290507362e-02, 1.19844998467991074170e-02 };
    double s = x - 1;
    double P = pa[0] + s * (pa[1] + s * (pa[2] + s * (pa[3] + s * (pa[4] + s * (pa[5] + s * pa[6])))));
    double Q = 1 + s * (qa[1] + s * (qa[2] + s * (qa[3] + s * (qa[4] + s * (qa[5] + s * qa[6])))));
    return P / Q;
}

// erf_small is erf(x) - x divided by x for |x| < 0.84375, a rational
// function of x squared.
static double erf_small(double x) {
    static const double pp[5] = { 1.28379167095512558561e-01, -3.25042107247001499370e-01, -2.84817495755985104766e-02,
                                  -5.77027029648944159157e-03, -2.37630166566501626084e-05 };
    static const double qq[6] = { 0, 3.97917223959155352819e-01, 6.50222499887672944485e-02, 5.08130628187576562776e-03,
                                  1.32494738004321644526e-04, -3.96022827877536812320e-06 };
    double z = x * x;
    double r = pp[0] + z * (pp[1] + z * (pp[2] + z * (pp[3] + z * pp[4])));
    double s = 1 + z * (qq[1] + z * (qq[2] + z * (qq[3] + z * (qq[4] + z * qq[5]))));
    return r / s;
}

static const double erx = 8.45062911510467529297e-01; // erf(1), less the part the rational function adds

// sn_math_Erf is the error function: 1 or -1 at an infinity, NaN for
// NaN, x times a constant below 2^-28, and the three ranges above.
double sn_math_Erf(double x) {
    const double efx = 1.28379167095512586316e-01, efx8 = 1.02703333676410069053e+00;
    if (x != x) {
        return x;
    }

    if (x > DBL_MAX) {
        return 1;
    }

    if (x < -DBL_MAX) {
        return -1;
    }

    bool neg = x < 0;
    double a = neg ? -x : x, r;
    if (a < 0.84375) {
        if (a < 1.0 / (1 << 28)) {
            r = a < 2.848094538889218e-306 ? 0.125 * (8.0 * a + efx8 * a) : a + efx * a; // the first form keeps a tiny a from underflowing
        } else {
            r = a + a * erf_small(a);
        }
    } else if (a < 1.25) {
        r = erx + erf_mid(a);
    } else if (a >= 6) {
        r = 1;
    } else {
        r = 1 - erf_tail(a);
    }

    return neg ? -r : r;
}

// sn_math_Erfc is the complementary error function, 1 - erf(x), exact
// where the subtraction would lose the digits: 0 at +Inf, 2 at -Inf,
// NaN for NaN, and 0 or 2 past 28 either way.
double sn_math_Erfc(double x) {
    if (x != x) {
        return x;
    }

    if (x > DBL_MAX) {
        return 0;
    }

    if (x < -DBL_MAX) {
        return 2;
    }

    bool neg = x < 0;
    double a = neg ? -x : x;
    if (a < 0.84375) {
        double t;
        if (a < 1.0 / ((uint64_t)1 << 56)) {
            t = a;
        } else {
            double y = erf_small(a);
            t = a < 0.25 ? a + a * y : 0.5 + (a * y + (a - 0.5));
        }

        return neg ? 1 + t : 1 - t;
    }

    if (a < 1.25) {
        double pq = erf_mid(a); // (1 - erx) - pq in this order: the rounding at the result's size, not at erx's
        return neg ? 1 + erx + pq : 1 - erx - pq;
    }

    if (a < 28) {
        if (neg && a > 6) {
            return 2;
        }

        double t = erf_tail(a);
        return neg ? 2 - t : t;
    }

    return neg ? 2 : 0;
}

// sn_math_Asinh is the inverse hyperbolic sine: log(2x) past 2^28,
// log(2x + 1/(sqrt(x^2+1) + x)) above 2, log1p of the small form
// between, and x itself below 2^-28; an infinity and NaN come back as
// they are.
double sn_math_Asinh(double x) {
    if (x != x || x > DBL_MAX || x < -DBL_MAX) {
        return x;
    }

    bool neg = x < 0;
    double a = neg ? -x : x, r;
    if (a > (double)(1 << 28)) {
        r = log(a) + 6.93147180559945286227e-01;
    } else if (a > 2) {
        r = log(2 * a + 1 / (sqrt(a * a + 1) + a));
    } else if (a < 1.0 / (1 << 28)) {
        r = a;
    } else {
        r = log1p(a + a * a / (1 + sqrt(1 + a * a)));
    }

    return neg ? -r : r;
}

// sn_math_Atanh is the inverse hyperbolic tangent: NaN outside [-1, 1]
// and for NaN, an infinity of the sign at 1 and -1, x itself below
// 2^-28, and half the log1p of the two forms above.
double sn_math_Atanh(double x) {
    if (x != x || x < -1 || x > 1) {
        return NAN;
    }

    if (x == 1 || x == -1) {
        return x > 0 ? INFINITY : -INFINITY;
    }

    bool neg = x < 0;
    double a = neg ? -x : x, r;
    if (a < 1.0 / (1 << 28)) {
        r = a;
    } else if (a < 0.5) {
        double t = a + a;
        r = 0.5 * log1p(t + t * a / (1 - a));
    } else {
        r = 0.5 * log1p((a + a) / (1 - a));
    }

    return neg ? -r : r;
}

// gamma_stirling is Stirling's formula for the gamma function of x
// above 33, in two factors whose product is the value, so that a
// quotient of them is taken before either overflows; above 200 the
// first factor is +Inf.
static void gamma_stirling(double x, double *y1, double *y2) {
    static const double gs[5] = { 7.87311395793093628397e-04, -2.29549961613378126380e-04, -2.68132617805781232825e-03,
                                  3.47222221605458667310e-03, 8.33333333333482257126e-02 };
    const double sqrtTwoPi = 2.506628274631000502417, maxStirling = 143.01608;
    if (x > 200) {
        *y1 = INFINITY;
        *y2 = 1;
        return;
    }

    double w = 1 / x;
    w = 1 + w * ((((gs[0] * w + gs[1]) * w + gs[2]) * w + gs[3]) * w + gs[4]);
    double e = exp(x), second = 1.0;
    if (x > maxStirling) { // pow would overflow: split the power
        double v = pow(x, 0.5 * x - 0.25);
        *y1 = v;
        second = v / e;
    } else {
        *y1 = pow(x, x - 0.5) / e;
    }

    *y2 = sqrtTwoPi * w * second;
}

// sn_math_Gamma is the gamma function: Stirling's formula above 33, the
// reflection formula below -33, and otherwise the argument brought into
// [2, 3) by the recurrence and a rational function there. +Inf for +0,
// -Inf for -0, NaN for a negative whole number, -Inf and NaN, +Inf for
// +Inf.
double sn_math_Gamma(double x) {
    static const double gp[7] = { 1.60119522476751861407e-04, 1.19135147006586384913e-03, 1.04213797561761569935e-02,
                                  4.76367800457137231464e-02, 2.07448227648435975150e-01, 4.94214826801497100753e-01,
                                  9.99999999999999996796e-01 };
    static const double gq[8] = { -2.31581873324120129819e-05, 5.39605580493303397842e-04, -4.45641913851797240494e-03,
                                  1.18139785222060435552e-02,  3.58236398605498653373e-02, -2.34591795718243348568e-01,
                                  7.14304917030273074085e-02,  1.00000000000000000320e+00 };
    const double euler = 0.57721566490153286060651209008240243104215933593992;
    if (x != x || x < -DBL_MAX || (x < 0 && x == floor(x))) {
        return NAN;
    }

    if (x > DBL_MAX) {
        return INFINITY;
    }

    if (x == 0) {
        return signbit(x) ? -INFINITY : INFINITY;
    }

    double q = fabs(x), p = floor(q);
    if (q > 33) {
        double y1, y2;
        if (x >= 0) {
            gamma_stirling(x, &y1, &y2);
            return y1 * y2;
        }

        double sign = fmod(p, 2) == 0 ? -1 : 1; // the gamma function changes sign at each negative whole number
        double z = q - p;
        if (z > 0.5) {
            p = p + 1;
            z = q - p;
        }

        z = q * sin(M_PI * z);
        if (z == 0) {
            return sign * INFINITY;
        }

        gamma_stirling(q, &y1, &y2);
        double absz = fabs(z), d = absz * y1 * y2;
        z = d > DBL_MAX ? M_PI / absz / y1 / y2 : M_PI / d;
        return sign * z;
    }

    double z = 1.0;
    for (; x >= 3; x = x - 1) {
        z = z * (x - 1);
    }

    for (; x < 0; x = x + 1) {
        if (x > -1e-09) {
            return z / ((1 + euler * x) * x);
        }

        z = z / x;
    }

    for (; x < 2; x = x + 1) {
        if (x < 1e-09) {
            return x == 0 ? INFINITY : z / ((1 + euler * x) * x);
        }

        z = z / x;
    }

    if (x == 2) {
        return z;
    }

    x = x - 2;
    double pn = (((((x * gp[0] + gp[1]) * x + gp[2]) * x + gp[3]) * x + gp[4]) * x + gp[5]) * x + gp[6];
    double qn = ((((((x * gq[0] + gq[1]) * x + gq[2]) * x + gq[3]) * x + gq[4]) * x + gq[5]) * x + gq[6]) * x + gq[7];
    return z * pn / qn;
}

// sin_pi is sin(pi * x) for a positive x, exact at the whole numbers
// and reduced modulo 2 before the library's sine, as the reflection
// formula of the logarithm of the gamma function needs it.
static double sin_pi(double x) {
    const double two52 = 4503599627370496.0, two53 = 9007199254740992.0;
    if (x < 0.25) {
        return -sin(M_PI * x);
    }

    double z = floor(x);
    int n;
    if (z != x) {
        x = fmod(x, 2);
        n = (int)(x * 4);
    } else if (x >= two53) { // a whole number this large is even
        x = 0;
        n = 0;
    } else {
        if (x < two52) {
            z = x + two52; // exact: the low bit of the sum is the parity of x
        }

        n = (int)(1 & sn_f64_bits(z));
        x = n;
        n <<= 2;
    }

    switch (n) {
    case 0:
        x = sin(M_PI * x);
        break;
    case 1:
    case 2:
        x = cos(M_PI * (0.5 - x));
        break;
    case 3:
    case 4:
        x = sin(M_PI * (1 - x));
        break;
    case 5:
    case 6:
        x = -cos(M_PI * (x - 1.5));
        break;
    default:
        x = sin(M_PI * (x - 2));
    }

    return -x;
}

// sn_math_lgamma is the natural logarithm of the absolute value of the
// gamma function of x, with the gamma function's sign stored, -1 or 1:
// polynomial and rational approximations in four ranges up to 8, the
// asymptotic series to 2^58 and x(log x - 1) past it, and the
// reflection formula for a negative x. +Inf at zero, at a negative
// whole number and at either infinity, NaN for NaN, the sign 1 at each.
double sn_math_lgamma(double x, int64_t *sign) {
    static const double la[12] = { 7.72156649015328655494e-02, 3.22467033424113591611e-01, 6.73523010531292681824e-02,
                                   2.05808084325167332806e-02, 7.38555086081402883957e-03, 2.89051383673415629091e-03,
                                   1.19270763183362067845e-03, 5.10069792153511336608e-04, 2.20862790713908385557e-04,
                                   1.08011567247583939954e-04, 2.52144565451257326939e-05, 4.48640949618915160150e-05 };
    static const double lr[7] = { 1.0, 1.39200533467621045958e+00, 7.21935547567138069525e-01, 1.71933865632803078993e-01,
                                  1.86459191715652901344e-02, 7.77942496381893596434e-04, 7.32668430744625636189e-06 };
    static const double ls[7] = { -7.72156649015328655494e-02, 2.14982415960608852501e-01, 3.25778796408930981787e-01,
                                  1.46350472652464452805e-01,  2.66422703033638609560e-02, 1.84028451407337715652e-03,
                                  3.19475326584100867617e-05 };
    static const double lt[15] = { 4.83836122723810047042e-01,  -1.47587722994593911752e-01, 6.46249402391333854778e-02,
                                   -3.27885410759859649565e-02, 1.79706750811820387126e-02,  -1.03142241298341437450e-02,
                                   6.10053870246291332635e-03,  -3.68452016781138256760e-03, 2.25964780900612472250e-03,
                                   -1.40346469989232843813e-03, 8.81081882437654011382e-04,  -5.38595305356740546715e-04,
                                   3.15632070903625950361e-04,  -3.12754168375120860518e-04, 3.35529192635519073543e-04 };
    static const double lu[6] = { -7.72156649015328655494e-02, 6.32827064025093366517e-01, 1.45492250137234768737e+00,
                                  9.77717527963372745603e-01,  2.28963728064692451092e-01, 1.33810918536787660377e-02 };
    static const double lv[6] = { 1.0, 2.45597793713041134822e+00, 2.12848976379893395361e+00, 7.69285150456672783825e-01,
                                  1.04222645593369134254e-01, 3.21709242282423911810e-03 };
    static const double lw[7] = { 4.18938533204672725052e-01,  8.33333333333329678849e-02, -2.77777777728775536470e-03,
                                  7.93650558643019558500e-04,  -5.95187557450339963135e-04, 8.36339918996282139126e-04,
                                  -1.63092934096575273989e-03 };
    const double ymin = 1.461632144968362245, two52 = 4503599627370496.0, two58 = 288230376151711744.0;
    const double tiny = 8.470329472543003e-22, tc = 1.46163214496836224576e+00, tf = -1.21486290535849611461e-01,
                 tt = -3.63867699703950536541e-18;
    *sign = 1;
    if (x != x) {
        return x;
    }

    if (x > DBL_MAX || x < -DBL_MAX) {
        return INFINITY;
    }

    if (x == 0) {
        return INFINITY;
    }

    bool neg = x < 0;
    if (neg) {
        x = -x;
    }

    if (x < tiny) { // below 2^-70 the logarithm of 1/|x|
        if (neg) {
            *sign = -1;
        }

        return -log(x);
    }

    double nadj = 0;
    if (neg) {
        if (x >= two52) { // a negative number this large is a whole one
            return INFINITY;
        }

        double t = sin_pi(x);
        if (t == 0) {
            return INFINITY;
        }

        nadj = log(M_PI / fabs(t * x));
        if (t < 0) {
            *sign = -1;
        }
    }

    double r;
    if (x == 1 || x == 2) {
        r = 0;
    } else if (x < 2) {
        double y;
        int i;
        if (x <= 0.9) {
            r = -log(x);
            if (x >= ymin - 1 + 0.27) {
                y = 1 - x;
                i = 0;
            } else if (x >= ymin - 1 - 0.27) {
                y = x - (tc - 1);
                i = 1;
            } else {
                y = x;
                i = 2;
            }
        } else {
            r = 0;
            if (x >= ymin + 0.27) {
                y = 2 - x;
                i = 0;
            } else if (x >= ymin - 0.27) {
                y = x - tc;
                i = 1;
            } else {
                y = x - 1;
                i = 2;
            }
        }

        if (i == 0) {
            double z = y * y;
            double p1 = la[0] + z * (la[2] + z * (la[4] + z * (la[6] + z * (la[8] + z * la[10]))));
            double p2 = z * (la[1] + z * (la[3] + z * (la[5] + z * (la[7] + z * (la[9] + z * la[11])))));
            r += y * p1 + p2 - 0.5 * y;
        } else if (i == 1) {
            double z = y * y, w = z * y;
            double p1 = lt[0] + w * (lt[3] + w * (lt[6] + w * (lt[9] + w * lt[12])));
            double p2 = lt[1] + w * (lt[4] + w * (lt[7] + w * (lt[10] + w * lt[13])));
            double p3 = lt[2] + w * (lt[5] + w * (lt[8] + w * (lt[11] + w * lt[14])));
            r += tf + (z * p1 - (tt - w * (p2 + y * p3)));
        } else {
            double p1 = y * (lu[0] + y * (lu[1] + y * (lu[2] + y * (lu[3] + y * (lu[4] + y * lu[5])))));
            double p2 = 1 + y * (lv[1] + y * (lv[2] + y * (lv[3] + y * (lv[4] + y * lv[5]))));
            r += -0.5 * y + p1 / p2;
        }
    } else if (x < 8) {
        int i = (int)x;
        double y = x - i;
        double p = y * (ls[0] + y * (ls[1] + y * (ls[2] + y * (ls[3] + y * (ls[4] + y * (ls[5] + y * ls[6]))))));
        double q = 1 + y * (lr[1] + y * (lr[2] + y * (lr[3] + y * (lr[4] + y * (lr[5] + y * lr[6])))));
        r = 0.5 * y + p / q;
        double z = 1.0; // the recurrence down from x to 2 + y, as a product of the factors
        for (int k = i - 1; k >= 2; k--) {
            z *= y + k;
        }

        if (i >= 3) {
            r += log(z);
        }
    } else if (x < two58) {
        double t = log(x), z = 1 / x, y = z * z;
        double w = lw[0] + z * (lw[1] + y * (lw[2] + y * (lw[3] + y * (lw[4] + y * (lw[5] + y * lw[6])))));
        r = (x - 0.5) * (t - 1) + w;
    } else {
        r = x * (log(x) - 1);
    }

    return neg ? nadj - r : r;
}

// sn_math_Pow10 is 10^n within one unit in the last place over the
// whole range of a double, and exact for n from 0 to 22, where a double
// holds the power: the product or quotient of two powers from two
// tables, 10^(32k) and 10^(n mod 32), each the nearest double; 0 below
// 10^-323 and +Inf above 10^308.
double sn_math_Pow10(int64_t n) {
    static const double tab[32] = { 1e00, 1e01, 1e02, 1e03, 1e04, 1e05, 1e06, 1e07, 1e08, 1e09, 1e10,
                                    1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21,
                                    1e22, 1e23, 1e24, 1e25, 1e26, 1e27, 1e28, 1e29, 1e30, 1e31 };
    static const double pos32[10] = { 1e00, 1e32, 1e64, 1e96, 1e128, 1e160, 1e192, 1e224, 1e256, 1e288 };
    static const double neg32[11] = { 1e-00, 1e-32, 1e-64, 1e-96, 1e-128, 1e-160, 1e-192, 1e-224, 1e-256, 1e-288, 1e-320 };
    if (n >= 0 && n <= 308) {
        return pos32[n / 32] * tab[n % 32];
    }

    if (n < 0 && n >= -323) {
        return neg32[-n / 32] / tab[-n % 32];
    }

    return n > 0 ? INFINITY : 0;
}

// The bits of a float and back, under the library's names, for a value
// of one of them; a call is written in place.
uint64_t sn_math_Float64bits(double x) { return sn_f64_bits(x); }
double sn_math_Float64frombits(uint64_t b) { return sn_f64_from_bits(b); }
uint32_t sn_math_Float32bits(float x) { return sn_f32_bits(x); }
float sn_math_Float32frombits(uint32_t b) { return sn_f32_from_bits(b); }

// sn_math__bits_mul64 is the 128-bit product of x and y, the high half
// returned and the low half stored: the header's, under the library's
// name.
uint64_t sn_math__bits_mul64(uint64_t x, uint64_t y, uint64_t *lo) { return sn_bits_Mul64(x, y, lo); }

// sn_math__bits_div64 divides the 128-bit number hi:lo by y, the
// quotient returned and the remainder stored; the library checked that
// y is not zero and that hi is below y, so the quotient fits. A
// processor with the instruction divides at once; without one, y is
// shifted up to its top bit and the dividend split into 32-bit halves,
// each quotient digit estimated by one 64-by-32 division and corrected
// at most twice, Knuth's algorithm D at base 2^32.
uint64_t sn_math__bits_div64(uint64_t hi, uint64_t lo, uint64_t y, uint64_t *rem) {
    if (hi == 0) {
        *rem = lo % y;
        return lo / y;
    }

#if defined(__x86_64__)
    uint64_t q, r;
    __asm__("divq %4" : "=a"(q), "=d"(r) : "a"(lo), "d"(hi), "rm"(y));
    *rem = r;
    return q;
#else
    const uint64_t two32 = (uint64_t)1 << 32, mask32 = two32 - 1;
    int s = __builtin_clzll(y);
    y <<= s;
    uint64_t yn1 = y >> 32, yn0 = y & mask32;
    uint64_t un32 = s == 0 ? hi : hi << s | lo >> (64 - s);
    uint64_t un10 = lo << s;
    uint64_t un1 = un10 >> 32, un0 = un10 & mask32;
    uint64_t q1 = un32 / yn1, rhat = un32 - q1 * yn1;
    while (q1 >= two32 || q1 * yn0 > two32 * rhat + un1) {
        q1--;
        rhat += yn1;
        if (rhat >= two32) {
            break;
        }
    }

    uint64_t un21 = un32 * two32 + un1 - q1 * y;
    uint64_t q0 = un21 / yn1;
    rhat = un21 - q0 * yn1;
    while (q0 >= two32 || q0 * yn0 > two32 * rhat + un0) {
        q0--;
        rhat += yn1;
        if (rhat >= two32) {
            break;
        }
    }

    *rem = (un21 * two32 + un0 - q0 * y) >> s;
    return q1 * two32 + q0;
#endif
}

// The functions of math/bits under the library's names, each the
// header's: a call the program writes is the instruction, written in
// its place, and these are what a value of one of them calls.
#define SN_BITS_NAMED(S, W, T)                                                                                         \
    int64_t sn_math__bits_LeadingZeros##S(T x) { return sn_bits_LeadingZeros##W(x); }                                  \
    int64_t sn_math__bits_TrailingZeros##S(T x) { return sn_bits_TrailingZeros##W(x); }                                \
    int64_t sn_math__bits_OnesCount##S(T x) { return sn_bits_OnesCount##W(x); }                                        \
    int64_t sn_math__bits_Len##S(T x) { return sn_bits_Len##W(x); }                                                    \
    T sn_math__bits_Reverse##S(T x) { return sn_bits_Reverse##W(x); }                                                  \
    T sn_math__bits_RotateLeft##S(T x, int64_t k) { return sn_bits_RotateLeft##W(x, k); }

SN_BITS_NAMED(, 64, uint64_t)
SN_BITS_NAMED(8, 8, uint8_t)
SN_BITS_NAMED(16, 16, uint16_t)
SN_BITS_NAMED(32, 32, uint32_t)
SN_BITS_NAMED(64, 64, uint64_t)

uint64_t sn_math__bits_ReverseBytes(uint64_t x) { return sn_bits_ReverseBytes64(x); }
uint16_t sn_math__bits_ReverseBytes16(uint16_t x) { return sn_bits_ReverseBytes16(x); }
uint32_t sn_math__bits_ReverseBytes32(uint32_t x) { return sn_bits_ReverseBytes32(x); }
uint64_t sn_math__bits_ReverseBytes64(uint64_t x) { return sn_bits_ReverseBytes64(x); }
